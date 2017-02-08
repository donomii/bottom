package main

import "github.com/mitchellh/go-ps"
import "strings"
import "fmt"
//import "os"
import "os/exec"
//import "time"




func getProcs() map[int]ps.Process {
    procs, _ := ps.Processes()
    procHash := map[int]ps.Process{}
    for _, v := range procs {
        procHash[v.Pid()] = v
    }
    return procHash
}

var deets map[int]string


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
    } else {
    #if runtime.GOOS == "darwin" {
    #if runtime.GOOS == "linux" {
        out := doCommand("ps", []string{ "-o", "command", "-p", fmt.Sprintf("%v",v.Pid())})
        return out
    }
}

func printHash(a string, b map[int]ps.Process) {
    for _, v := range b {
        fmt.Printf("(%v %v)     ", a, v.Executable())
        if a == "Start: " {
            out = strings.Replace(out, "  PID TTY           TIME CMD\n", "", -1)
            out = strings.Replace(out, "COMMAND\n", "", -1)
            fmt.Println(out)
            deets[v.Pid()] = out
        }
        if a == "Stop:  " {
            fmt.Println(deets[v.Pid()])
        }
        /*
        Does nothing on OSX
        p, _ := os.FindProcess(v.Pid())
        fmt.Println(p)
        */
        
    }

}

func main () {
    deets = map[int]string{}
    procs := getProcs()
    for {
        //time.Sleep(1 * time.Second)
        new := getProcs()
        diff := hashDiff(new, procs)
        printHash("Start: ", diff)
        diff = hashDiff(procs, new)
        printHash("Stop: ", diff)
        procs = new
    }

}
