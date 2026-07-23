package main

import (
	"strconv"
	"strings"
)

func tuiSearchText(event Event) string {
	parts := []string{
		string(event.Kind),
		event.ProcessID,
		event.Command,
		event.Exe,
		event.Cwd,
		event.User,
		event.UID,
		event.TTY,
		event.Session,
		strconv.Itoa(event.PID),
		strconv.Itoa(event.ParentPID),
		event.Message,
	}
	for _, parent := range event.ParentChain {
		parts = append(parts, parent.Command, parent.Exe, parent.User, strconv.Itoa(parent.PID))
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}
