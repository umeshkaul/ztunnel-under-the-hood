package main

import (
    "fmt"
    "net"
    "os"

    "golang.org/x/sys/unix"
)

func main() {
    if len(os.Args) != 3 {
        fmt.Fprintf(os.Stderr, "usage: %s <uds_path> <netns_path>\n", os.Args[0])
        os.Exit(1)
    }
    uds, nsPath := os.Args[1], os.Args[2]

    c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: uds, Net: "unix"})
    if err != nil { panic(err) }
    defer c.Close()

    f, err := os.Open(nsPath)
    if err != nil { panic(err) }
    defer f.Close()

    rights := unix.UnixRights(int(f.Fd()))
    if _, _, err := c.WriteMsgUnix([]byte{1}, rights, nil); err != nil { panic(err) }
}

