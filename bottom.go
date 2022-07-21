package main

//import "syscall"
import (
	"flag"
	"fmt"
	"github.com/donomii/goof"
	"github.com/mitchellh/go-ps"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

//import "os"

var deets map[int]string
var summary map[string]int
var badProgs = []string{}

type psdeets struct {
	Pid     int
	Command string
}

func count(s string) {
	summary[s] = summary[s] + 1
}

func getProcs() map[int]ps.Process {
	procs, _ := ps.Processes()
	procHash := map[int]ps.Process{}
	for _, v := range procs {
		procHash[v.Pid()] = v
	}
	return procHash
}

func hashDiff(a, b map[int]psdeets) map[int]psdeets {
	retHash := map[int]psdeets{}
	for k, v := range a {
		if _, ok := b[k]; ok {
			//fmt.Println("Key ", k, " present in both")
			//do something here
		} else {
			//fmt.Println("Key ", k, " not present in both")
			retHash[k] = v
		}
	}
	return retHash
}

func doCommand(cmd string, args []string) string {
	out, err := exec.Command(cmd, args...).CombinedOutput()
	if err != nil {
		//fmt.Fprintf(os.Stderr, "Output: %v", string(out))
		//fmt.Fprintf(os.Stderr, "Error: %v", err)
		//os.Exit(1)
	}
	if string(out) != "" {
		//fmt.Fprintf(os.Stderr, "Output: %v\n\n", string(out))
	}
	return string(out)
}

func extendedPS(pid int) string {
	if runtime.GOOS == "windows" {
		cmd := exec.Command("tasklist.exe", "/fo", "csv", "/nh")
		//Only compiles on windows?
		//cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return string(out)
	} else {
		//if runtime.GOOS == "darwin" {
		//if runtime.GOOS == "linux" {
		out := doCommand("ps", []string{"-o", "command", "-p", fmt.Sprintf("%v", pid)})
		return out
	}
	return ""
}

func simplePSUnix() map[int]psdeets {
	outstr := goof.Shell("ps -eo pid,command")
	lines := strings.Split(outstr, "\n")
	out := map[int]psdeets{}
	//Iterate over lines, split on spaces
	for _, line := range lines {
		parts := strings.SplitN(line, " ", 2)
		if len(parts) > 1 {
			p := psdeets{Pid: goof.Atoi(parts[0]), Command: parts[1]}
			out[p.Pid] = p
		}
	}
	return out
}

func simpleLsofUnix() map[int]psdeets {
	outstr := goof.Shell("lsof -n -Fn")
	lines := strings.Split(outstr, "\n")
	out := map[int]psdeets{}
	pos := 0
	//Iterate over lines, read into a hash of hashes, keyed by pid
	for {
		if pos >= len(lines) {
			break
		}
		line := lines[pos]
		procHash := map[string]string{}
		if strings.HasPrefix(line, "p") {
			line = strings.TrimPrefix(line, "p")
			pid := goof.Atoi(line)
			for {
				pos++
				if pos >= len(lines) {
					break
				}
				line = lines[pos]
				if strings.HasPrefix(line, "p") {
					break
				}
				if strings.HasPrefix(line, "f") {
					fieldname := strings.TrimPrefix(line, "f")
					pos++
					val := lines[pos]
					val = strings.TrimPrefix(val, "n")
					//If fieldname is not in the hash already, add it
					if _, ok := procHash[fieldname]; !ok {
						procHash[fieldname] = val
					}
				}
			}
			out[pid] = psdeets{Pid: pid, Command: procHash["txt"]}
		}
		pos++
	}
	return out
}

func chomp(s string) string {
	return strings.TrimSuffix(s, "\n")
}

func printHash(a string, b map[int]psdeets) {
	for pid, v := range b {
		if v.Command == "ps" || strings.Contains(v.Command, " ps ") || strings.Contains(v.Command, "\tps\t") {
			fmt.Println("")
			continue
		}
		//fmt.Printf("%8v %8v %-20v ", a, v.Pid, v.Command)
		count(v.Command)

		if a == "Start:" {
			//out := chomp(chomp(extendedPS(v.Pid)))
			out := v.Command
			out = strings.Replace(out, "  PID TTY           TIME CMD\n", "", -1)
			out = strings.Replace(out, "COMMAND\n", "", -1)
			out = fmt.Sprintf("%v: %v", pid, out)
			if strings.Contains(out, "ps -eo pid,command") {
				continue
			}
			fmt.Printf("Start: %v\n", out)
			deets[v.Pid] = out
		}
		if a == "Stop:" {
			if strings.Contains(v.Command, "ps -eo pid,command") {
				continue
			}
			fmt.Printf("Stop: %v\n", deets[v.Pid])
		}
		//fmt.Println("")
		/*
		   Does nothing on OSX
		   p, _ := os.FindProcess(v.Pid())
		   fmt.Println(p)
		*/

	}

}

func summaryPrint() {
	for {
		time.Sleep(1 * time.Second)
		fmt.Println("Summary\n", summary)
	}
}

func main() {
	fmt.Println("Starting process watch")
	flag.Parse()
	badProgs = flag.Args()
	deets = map[int]string{}
	summary = map[string]int{}
	procs := simplePSUnix()

	//go summaryPrint()
	for {
		time.Sleep(1 * time.Second)

		//Detect new processes
		new := simplePSUnix()
		diff := hashDiff(new, procs)
		printHash("Start:", diff)
		//Detect dead processes
		diff = hashDiff(procs, new)
		printHash("Stop:", diff)
		procs = new

		//Search process list for bad programs.  Use the real path to the program
		//to catch programs that modify their command line
		pr := simpleLsofUnix()
		ownPid := os.Getpid()
		//Iterate over procs
		for _, p := range pr {
			if p.Pid == ownPid {
				continue
			}
			for _, bad := range badProgs {
				//log.Printf("Comparing %v to %v\n", bad, p.Command)
				if strings.Contains(p.Command, bad) {
					go func(p psdeets) {
						log.Printf("Found bad program %v, killing it: (%v)%v\n", bad, p.Pid, p.Command)
						goof.Shell(fmt.Sprintf("kill -1 %v", p.Pid))
						time.Sleep(1 * time.Second)
						goof.Shell(fmt.Sprintf("kill -15 %v", p.Pid))
						time.Sleep(1 * time.Second)
						goof.Shell(fmt.Sprintf("kill -9 %v", p.Pid))
						time.Sleep(1 * time.Second)
						goof.Shell(fmt.Sprintf("kill -11 %v", p.Pid))

					}(p)

				}
			}
		}
	}

}
