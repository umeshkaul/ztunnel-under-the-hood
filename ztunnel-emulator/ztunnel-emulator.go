package main

import (
    "fmt"
    "net"
    "os"
    "runtime"

    "github.com/vishvananda/netns"
    "golang.org/x/sys/unix"
)

func recvFD(c *net.UnixConn) (int, error) {
    b := make([]byte, 1)
    oob := make([]byte, unix.CmsgSpace(4))
    _, oobn, _, _, err := c.ReadMsgUnix(b, oob)
    if err != nil { return -1, err }
    scms, err := unix.ParseSocketControlMessage(oob[:oobn])
    if err != nil || len(scms) == 0 { return -1, fmt.Errorf("no cmsg") }
    fds, err := unix.ParseUnixRights(&scms[0])
    if err != nil || len(fds) == 0 { return -1, fmt.Errorf("no fd") }
    return fds[0], nil
}

func main() {
    if len(os.Args) != 2 {
        fmt.Fprintf(os.Stderr, "usage: %s <uds_path>\n", os.Args[0])
        os.Exit(1)
    }
    uds := os.Args[1]
    _ = os.Remove(uds)
    l, err := net.ListenUnix("unix", &net.UnixAddr{Name: uds, Net: "unix"})
    if err != nil { panic(err) }
    defer os.Remove(uds)

    conn, err := l.AcceptUnix()
    if err != nil { panic(err) }
    nsfd, err := recvFD(conn)
    if err != nil { panic(err) }

    runtime.LockOSThread()
    defer runtime.UnlockOSThread()
    cur, _ := netns.Get()
    defer cur.Close()
    if err := unix.Setns(nsfd, unix.CLONE_NEWNET); err != nil { panic(err) }

    ports := []string{"15008", "15006", "15001"}
    var listeners []net.Listener
    for _, p := range ports {
        ln, err := net.Listen("tcp4", "127.0.0.1:"+p)
        if err != nil { panic(err) }
        listeners = append(listeners, ln)
    }
    _ = netns.Set(cur) // back to original netns; sockets stay in target

    for _, ln := range listeners {
        go func(l net.Listener) {
            for {
                c, err := l.Accept()
                if err != nil { return }
                c.Write([]byte("ok\n"))
                c.Close()
            }
        }(ln)
    }
    fmt.Println("listeners ready")
    select {}
}

