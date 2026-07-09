//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

const (
	cnIdxProc          = 1
	cnValProc          = 1
	procCnMcastListen  = 1
	procCnMcastIgnore  = 2
	procEventFork      = 0x00000001
	procEventExec      = 0x00000002
	procEventExit      = 0x80000000
	procConnectorData  = 20
	procEventUnionData = 16
)

type LinuxProcConnectorBackend struct {
	interval time.Duration
}

type connectorNotice struct {
	pid      int
	exitCode *int
}

func NewPlatformEventBackend(config Config) (LifecycleBackend, error) {
	return NewNamedEventBackend(Config{Backend: BackendLinuxProcConnector, PollInterval: config.PollInterval})
}

func NewNamedEventBackend(config Config) (LifecycleBackend, error) {
	switch config.Backend {
	case BackendLinuxProcConnector:
		return LinuxProcConnectorBackend{interval: config.PollInterval}, nil
	default:
		return nil, fmt.Errorf("unknown event backend %q", config.Backend)
	}
}

func (backend LinuxProcConnectorBackend) Name() string {
	return BackendLinuxProcConnector
}

func (backend LinuxProcConnectorBackend) Watch(ctx context.Context, events chan<- Event) error {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM, syscall.NETLINK_CONNECTOR)
	if err != nil {
		return fmt.Errorf("open netlink connector socket for process events: %w", err)
	}
	defer syscall.Close(fd)
	timeout := syscall.NsecToTimeval(int64(time.Second))
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &timeout); err != nil {
		return fmt.Errorf("set netlink connector receive timeout: %w", err)
	}
	addr := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: cnIdxProc, Pid: uint32(os.Getpid())}
	if err := syscall.Bind(fd, addr); err != nil {
		return fmt.Errorf("bind netlink connector socket to process event group: %w", err)
	}
	if err := sendProcConnectorControl(fd, procCnMcastListen); err != nil {
		return fmt.Errorf("subscribe to process connector events: %w", err)
	}
	defer sendProcConnectorControl(fd, procCnMcastIgnore)
	previous, err := ReadProcessSnapshot()
	if err != nil {
		return fmt.Errorf("read initial process snapshot for proc connector backend: %w", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			notices, err := receiveProcConnectorNotices(fd)
			if err != nil {
				if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
					continue
				}
				return err
			}
			if len(notices) == 0 {
				continue
			}
			next, err := ReadProcessSnapshot()
			if err != nil {
				sendEvent(ctx, events, Event{
					Kind:    EventGap,
					Time:    time.Now(),
					Backend: backend.Name(),
					Message: fmt.Sprintf("process connector event received but snapshot refresh failed; expected a complete resync, received error %v", err),
				})
				continue
			}
			emitSnapshotDiffWithExitCodes(ctx, backend.Name(), previous, next, events, exitCodesByPID(notices))
			previous = next
		}
	}
}

func sendProcConnectorControl(fd int, op uint32) error {
	request := make([]byte, syscall.NLMSG_HDRLEN+procConnectorData+4)
	binary.LittleEndian.PutUint32(request[0:4], uint32(len(request)))
	binary.LittleEndian.PutUint16(request[4:6], syscall.NLMSG_DONE)
	binary.LittleEndian.PutUint32(request[8:12], 1)
	binary.LittleEndian.PutUint32(request[12:16], uint32(os.Getpid()))
	cn := syscall.NLMSG_HDRLEN
	binary.LittleEndian.PutUint32(request[cn:cn+4], cnIdxProc)
	binary.LittleEndian.PutUint32(request[cn+4:cn+8], cnValProc)
	binary.LittleEndian.PutUint16(request[cn+16:cn+18], 4)
	binary.LittleEndian.PutUint32(request[cn+procConnectorData:cn+procConnectorData+4], op)
	return syscall.Sendto(fd, request, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK})
}

func receiveProcConnectorNotices(fd int) ([]connectorNotice, error) {
	buffer := make([]byte, 8192)
	n, _, err := syscall.Recvfrom(fd, buffer, 0)
	if err != nil {
		return nil, fmt.Errorf("receive process connector event: %w", err)
	}
	return parseProcConnectorNotices(buffer[:n]), nil
}

func parseProcConnectorNotices(buffer []byte) []connectorNotice {
	notices := []connectorNotice{}
	for offset := 0; offset+syscall.NLMSG_HDRLEN <= len(buffer); {
		length := int(binary.LittleEndian.Uint32(buffer[offset : offset+4]))
		if length < syscall.NLMSG_HDRLEN || offset+length > len(buffer) {
			break
		}
		notices = append(notices, parseProcConnectorMessage(buffer[offset+syscall.NLMSG_HDRLEN:offset+length])...)
		offset += alignNetlinkLength(length)
	}
	return notices
}

func parseProcConnectorMessage(payload []byte) []connectorNotice {
	if len(payload) < procConnectorData+procEventUnionData {
		return []connectorNotice{}
	}
	length := int(binary.LittleEndian.Uint16(payload[16:18]))
	if length < procEventUnionData || procConnectorData+length > len(payload) {
		return []connectorNotice{}
	}
	event := payload[procConnectorData : procConnectorData+length]
	what := binary.LittleEndian.Uint32(event[0:4])
	union := event[procEventUnionData:]
	switch what {
	case procEventFork:
		if len(union) < 16 {
			return []connectorNotice{}
		}
		return []connectorNotice{{pid: int(binary.LittleEndian.Uint32(union[8:12]))}}
	case procEventExec:
		if len(union) < 8 {
			return []connectorNotice{}
		}
		return []connectorNotice{{pid: int(binary.LittleEndian.Uint32(union[0:4]))}}
	case procEventExit:
		if len(union) < 16 {
			return []connectorNotice{}
		}
		code := int(int32(binary.LittleEndian.Uint32(union[8:12])))
		return []connectorNotice{{pid: int(binary.LittleEndian.Uint32(union[0:4])), exitCode: &code}}
	default:
		return []connectorNotice{}
	}
}

func alignNetlinkLength(length int) int {
	return (length + 3) & ^3
}

func exitCodesByPID(notices []connectorNotice) map[int]int {
	codes := map[int]int{}
	for _, notice := range notices {
		if notice.exitCode != nil {
			codes[notice.pid] = *notice.exitCode
		}
	}
	return codes
}
