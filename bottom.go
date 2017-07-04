package main

//import "syscall"
import "github.com/mitchellh/go-ps"
import "runtime"
import "strings"
import "fmt"
//import "os"
import "os/exec"
import "time"



var deets map[int]string
var summary map[string]int

func count (s string) {
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



func hashDiff(a, b map[int]ps.Process) map[int]ps.Process {
    retHash := map[int]ps.Process{}
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
        out := doCommand("ps", []string{ "-o", "command", "-p", fmt.Sprintf("%v",pid)})
        return out
    }
    return ""
}

func chomp(s string) string {
    return strings.TrimSuffix(s, "\n")
}

func printHash(a string, b map[int]ps.Process) {
    for _, v := range b {
        fmt.Printf("%8v %8v %-20v ", a, v.Pid(), v.Executable())
        count(v.Executable())
        if a == "Start:" {
            out := chomp(chomp(extendedPS(v.Pid())))
            out = strings.Replace(out, "  PID TTY           TIME CMD\n", "", -1)
            out = strings.Replace(out, "COMMAND\n", "", -1)
            fmt.Printf("%v", out)
            deets[v.Pid()] = out
        }
        if a == "Stop:" {
            fmt.Printf("%v", deets[v.Pid()])
        }
        fmt.Println("")
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

func main () {
    fmt.Println("Starting process watch")
    deets = map[int]string{}
    summary = map[string]int{}
    procs := getProcs()
    go summaryPrint()
    for {
        //time.Sleep(1 * time.Second)
        new := getProcs()
        diff := hashDiff(new, procs)
        printHash("Start:", diff)
        diff = hashDiff(procs, new)
        printHash("Stop:", diff)
        procs = new
    }

}
