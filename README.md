### Ztunnel Under the Hood: A Deep Dive into Istio's Ambient Mode Networking

## What You'll Learn

- How Istio eliminates sidecar containers while maintaining secure networking
- The Linux kernel magic that allows processes to "teleport" between namespaces
- Step-by-step implementation of a real-world networking concept
- Why this approach is more efficient than traditional service mesh architectures

## Background

This post explores the core networking technology behind Istio's ztunnel, a key component of its ambient mesh. Unlike the traditional sidecar model that injects a separate container into each pod, ztunnel operates as a single DaemonSet on each node. This approach allows it to handle the networking for all pods on that node, simplifying the architecture and improving efficiency.

The unique part of ztunnel's design is how it intercepts and manages traffic without using the common `veth` pairs, some kind of vpn tunnel or running a full sidecar proxy. Instead, it leverages powerful, low-level Linux kernel features to directly manage sockets in the network namespace of each application pod.

## Ztunnel's Kernel Magic Explained

At the heart of this capability are two key Linux features:

**Network Namespaces:** The Linux kernel isolates the network stack of each pod into a separate network namespace. These namespaces are treated as kernel objects and can be referenced.

**`setns()` System Call:** A privileged process, like ztunnel's node agent, can use the `setns()` system call to temporarily enter another process's namespace.

The ztunnel process combines these features to "teleport" into a pod's namespace, create the necessary listening sockets, and then return to its own namespace. Because the sockets are "pinned" to the pod's namespace, they remain active and functional there. This allows ztunnel to listen for and redirect traffic to the pod's workload without the need for a sidecar container. Traffic redirection is then handled by other kernel features like iptables TPROXY or eBPF.

This repository serves as a practical demonstration of these exact concepts, providing a concrete example of how a process can create and manage sockets in a separate network namespace. The code in the repository is a great way to see how these advanced kernel features work in a simplified, real-world context.

## The Istio Solution

As explained in [Howard John's excellent blog post on ztunnel's architecture](https://blog.howardjohn.info/posts/ztunnel-compute-traffic-view/) and [Istio documentation](https://istio.io/latest/docs/ambient/architecture/traffic-redirection/#:~:text=Once%20the%20istio,node%2Dlocal%20ztunnel), Istio solves this through a clever handoff mechanism:

1. **CNI Node Agent** (`istio-cni`) opens the pod's network namespace file
2. **CNI Node Agent** sends the network namespace file descriptor to ztunnel via Unix Domain Socket using `SCM_RIGHTS`
3. **Ztunnel** receives the file descriptor and uses `setns()` to enter the pod's network namespace
4. **Ztunnel** creates listening sockets on ports 15008, 15006, and 15001 inside the pod's namespace
5. **Ztunnel** returns to its original namespace while the sockets remain bound to the pod's namespace

The key insight is that while sockets are created in the target namespace, they remain valid and accessible from the host namespace.

## Emulating the Istio Solution for fun :) 

### What This Demo Shows

**Goal:** Make a process ("ztunnel") create **listeners inside a pod's netns** after a **CNI-like sender** provides a **netns file descriptor (FD)** over a **Unix Domain Socket (UDS)**—just like Istio ambient mode describes.

**Mapping:**

- **`cni-emulator`** ⇢ the **istio-cni node agent**: opens the pod's netns file (e.g., `/var/run/netns/ns2`) and **sends that FD** to ztunnel via UDS using `SCM_RIGHTS`.
- **`ztunnel-emulator`** ⇢ the **ztunnel proxy**: **receives the netns FD**, temporarily **`setns()` into that netns**, **binds listeners** on `127.0.0.1:{15008,15006,15001}` inside the pod netns, then returns to its own netns. The sockets remain attached to the **pod netns**.

### High level Overview 

```
┌─────────────────────────────────────────────────────────────────────────────────┐
│                              DEMO OVERVIEW                                      │
│                    (How CNI-ztunnel Handoff Works)                              │
└─────────────────────────────────────────────────────────────────────────────────┘

┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐
│   CNI Agent     │    │  Ztunnel        │    │  Pod Namespace  │
│   (cni-emulator)│    │  (ztunnel-emul.)│    │     (ns2)       │
└─────────────────┘    └─────────────────┘    └─────────────────┘
         │                       │                       │
         │ 1. Open /var/run/     │                       │
         │    netns/ns2          │                       │
         │                       │                       │
         │ 2. Send FD via UDS    │                       │
         │    SCM_RIGHTS         │                       │
         ├──────────────────────▶│                       │
         │                       │                       │
         │                       │ 3. setns() into ns2   │
         │                       ├──────────────────────▶│
         │                       │                       │
         │                       │ 4. Create listeners   │
         │                       │    127.0.0.1:15008    │
         │                       │    127.0.0.1:15006    │
         │                       │    127.0.0.1:15001    │
         │                       │                       │
         │                       │ 5. Return to host NS  │
         │                       │◀──────────────────────┤
         │                       │                       │
         │                       │                       │
         │                       │                       ▼
         │                       │              ┌─────────────────┐
         │                       │              │ Listeners stay  │
         │                       │              │ in pod namespace│
         │                       │              │ (sockets remain │
         │                       │              │  bound to ns2)  │
         │                       │              └─────────────────┘

┌─────────────────────────────────────────────────────────────────────────────────┐
│                              KEY RESULT                                         │
│                                                                                 │
│  Ztunnel process (running in host namespace) now has active listeners           │
│  inside the pod's namespace (ns2). These listeners can accept connections       │
│  from applications running inside the pod, enabling sidecar-less networking.    │
└─────────────────────────────────────────────────────────────────────────────────┘
```
## Code Overview

### CNI Emulator (`cni-emulator/cni-emulator.go`)

Simulates the `istio-cni` node agent:

- **Connects** to ztunnel's Unix Domain Socket
- **Opens** the pod's network namespace file
- **Sends** the file descriptor to ztunnel using `SCM_RIGHTS`

```go
// Key code snippet
f, err := os.Open(nsPath)  // Open pod's netns file
rights := unix.UnixRights(int(f.Fd()))  // Prepare FD for transfer
c.WriteMsgUnix([]byte{1}, rights, nil)  // Send FD via UDS
```

### Ztunnel Emulator (`ztunnel-emulator/ztunnel-emulator.go`)

Simulates the ztunnel proxy:

- **Listens** on Unix Domain Socket for CNI connections
- **Receives** the network namespace file descriptor
- **Enters** the pod's namespace using `setns()`
- **Creates** listening sockets on ports 15008, 15006, 15001
- **Returns** to original namespace (sockets remain in pod namespace)

```go
// Key code snippet
nsfd, err := recvFD(conn)  // Receive netns FD from CNI
unix.Setns(nsfd, unix.CLONE_NEWNET)  // Enter pod's netns
// Create listeners on 15008, 15006, 15001
net.Listen("tcp4", "127.0.0.1:"+port)
```

## Walk-thru


### Prerequisites

- Linux system with network namespace support
- Go 1.24+
- Root privileges or appropriate capabilities

### 1. Build the Components

```bash
# Build CNI emulator
cd cni-emulator
go build -o cni-emulator cni-emulator.go

# Build ztunnel emulator
cd ../ztunnel-emulator  
go build -o ztunnel-emulator ztunnel-emulator.go
```

### 2. Set Capabilities (if not running as root)

```bash
sudo setcap cap_sys_admin+ep ./ztunnel-emulator
```

### 3. Create Test Network Namespace

```bash
$> sudo ip netns add ns2

$> sudo ip -n ns2 link set lo up

$> sudo ip -n ns2 link show
1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN mode DEFAULT group default qlen 1000
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
```

### 4. Start Ztunnel Emulator

```bash
$> ls -l
total 28
drwxrwxr-x 2 ukaul ukaul  4096 Sep 17 21:37 cni-emulator
-rw-rw-r-- 1 ukaul ukaul  5567 Sep 17 22:15 LOG.md
-rw-rw-r-- 1 ukaul ukaul 11431 Sep 17 22:23 README.md
drwxrwxr-x 4 ukaul ukaul  4096 Sep 17 21:38 ztunnel-emulator

$> sudo ./ztunnel-emulator/ztunnel-emulator /tmp/zt.sock &
[1] 3074196

$> ps -ef | egrep -i ztunnel-emulator
root     3074196 3031603  0 22:01 pts/20   00:00:00 sudo ./ztunnel-emulator/ztunnel-emulator /tmp/zt.sock
root     3074205 3074196  0 22:01 pts/25   00:00:00 sudo ./ztunnel-emulator/ztunnel-emulator /tmp/zt.sock
root     3074206 3074205  0 22:01 pts/25   00:00:00 ./ztunnel-emulator/ztunnel-emulator /tmp/zt.sock
ukaul    3074493 3031603  0 22:01 pts/20   00:00:00 grep --color=auto --exclude-dir=.bzr --exclude-dir=CVS --exclude-dir=.git --exclude-dir=.hg --exclude-dir=.svn --exclude-dir=.idea --exclude-dir=.tox --exclude-dir=.venv --exclude-dir=venv -E -i ztunnel-emulator
```

### 5. Send Network Namespace (CNI Step)

```bash
$> sudo ip netns exec ns2 ss -ntlp
State       Recv-Q       Send-Q             Local Address:Port             Peer Address:Port      Process       

$> sudo ./cni-emulator/cni-emulator /tmp/zt.sock /var/run/netns/ns2
listeners ready

$> sudo ip netns exec ns2 ss -ntlp
[sudo] password for ukaul: 
State   Recv-Q  Send-Q   Local Address:Port    Peer Address:Port Process                                        
LISTEN  0       4096         127.0.0.1:15008        0.0.0.0:*     users:(("ztunnel-emulato",pid=3074206,fd=9))  
LISTEN  0       4096         127.0.0.1:15006        0.0.0.0:*     users:(("ztunnel-emulato",pid=3074206,fd=10)) 
LISTEN  0       4096         127.0.0.1:15001        0.0.0.0:*     users:(("ztunnel-emulato",pid=3074206,fd=11)) 
```

### 6. Verify Listeners in Pod Namespace

```bash
# Launch a shell into the namesapce 

$> sudo ip netns exec ns2 bash
    
root$ ss -ntlp
State       Recv-Q      Send-Q           Local Address:Port            Peer Address:Port     Process                                            
LISTEN      0           4096                 127.0.0.1:15008                0.0.0.0:*         users:(("ztunnel-emulato",pid=3074206,fd=9))      
LISTEN      0           4096                 127.0.0.1:15006                0.0.0.0:*         users:(("ztunnel-emulato",pid=3074206,fd=10))     
LISTEN      0           4096                 127.0.0.1:15001                0.0.0.0:*         users:(("ztunnel-emulato",pid=3074206,fd=11))     

# Test connectivity

root$ nc -v -z localhost 15008
Connection to localhost (127.0.0.1) 15008 port [tcp/*] succeeded!
 
root$ nc -v -z localhost 15006
Connection to localhost (127.0.0.1) 15006 port [tcp/*] succeeded!
 
root$ nc -v -z localhost 15001
Connection to localhost (127.0.0.1) 15001 port [tcp/*] succeeded!
 
root$ nc -v -z localhost 15002
nc: connect to localhost (127.0.0.1) port 15002 (tcp) failed: Connection refused
```

### 7. Cleanup
```bash
# Remove test namespace
sudo ip netns delete ns2

# Stop ztunnel emulator
sudo pkill ztunnel-emulator
```
## References

- [Istio Ambient Traffic Redirection](https://istio.io/latest/docs/ambient/architecture/traffic-redirection/)
- [Istio CNI-Ztunnel Communication](https://istio.io/latest/docs/ambient/architecture/traffic-redirection/#:~:text=Once%20the%20istio,node%2Dlocal%20ztunnel)
- [Unix Domain Sockets and File Descriptor Passing](https://man7.org/linux/man-pages/man7/unix.7.html)
- [Network Namespaces](https://man7.org/linux/man-pages/man7/namespaces.7.html)
- https://blog.howardjohn.info/posts/ztunnel-compute-traffic-view/
- https://www.solo.io/blog/understanding-istio-ambient-ztunnel-and-secure-overlay
- https://istio.io/latest/blog/2023/rust-based-ztunnel/