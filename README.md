# Ztunnel Under the Hood

A demonstration of how Istio's ambient mode CNI-ztunnel-pod communication works, specifically focusing on how Ztunnel pod routes traffic between pods in different namespaces without creating tunnels. 

## Background

I started with a fundamental question about Istio's ambient mode: *"How does ztunnel route traffic between pods running in different namespaces on the same node over mTLS without having sidecars?"* 

In traditional service mesh architectures, each application pod runs a sidecar proxy to handle traffic interception, encryption, and routing. However, Istio's ambient mode introduces **ztunnel**, a per-node proxy that eliminates the need for sidecars while still ensuring secure mTLS communication.

This raises an intriguing architectural question: **How does ztunnel, which runs in its own network namespace as a single pod on the node, manage to intercept and route mTLS traffic for multiple application pods residing in separate network namespaces?**

As explained in [Howard John's excellent blog post on ztunnel's architecture](https://blog.howardjohn.info/posts/ztunnel-compute-traffic-view/),the key insight is that ztunnel achieves this by temporarily entering the network namespace of each pod it manages to open listening sockets within those namespaces. This allows ztunnel to handle traffic as if it were operating within each pod's network environment.

My question then became: *"How can I simulate this network namespace handoff mechanism with simple Go code?"* The answer involves two programs:

* One acts like the **CNI node agent**, opening the pod's netns file and sending that file descriptor to ztunnel via Unix Domain Socket
* The other acts like **ztunnel**, receiving the file descriptor, using `setns()` to enter the pod's namespace, and binding listeners inside it

This is exactly what I'm demonstrating below: showing how the CNI-ztunnel handoff enables a single process to create sockets inside multiple pod namespaces, enabling the sidecar-less mTLS architecture that makes Istio ambient mode so powerful.

## The Core Problem

The challenge ztunnel faces is architectural: **How can a single process running in one network namespace create listening sockets inside multiple other network namespaces?**

This is crucial for Istio ambient mode because:
- Ztunnel runs as a single pod per node in its own network namespace
- Application pods run in separate, isolated network namespaces  
- All inter-pod traffic must be mTLS-encrypted, even between pods on the same node
- Ztunnel needs to intercept traffic from each pod as if it were a sidecar within that pod

The fundamental constraint is that **sockets always belong to the namespace in which they are created**. You can't simply tell a process running in the host namespace to "go listen on port 15006 inside that pod's namespace" — the process needs to actually be inside that namespace when it creates the socket.

## The Istio Solution

As described in the [Istio documentation](https://istio.io/latest/docs/ambient/architecture/traffic-redirection/#:~:text=Once%20the%20istio,node%2Dlocal%20ztunnel), Istio solves this through a clever handoff mechanism:

1. **CNI Node Agent** (`istio-cni`) opens the pod's network namespace file
2. **CNI Node Agent** sends the network namespace file descriptor to ztunnel via Unix Domain Socket using `SCM_RIGHTS`
3. **Ztunnel** receives the file descriptor and uses `setns()` to enter the pod's network namespace
4. **Ztunnel** creates listening sockets on ports 15008, 15006, and 15001 inside the pod's namespace
5. **Ztunnel** returns to its original namespace while the sockets remain bound to the pod's namespace

The key insight is that while sockets are created in the target namespace, they remain valid and accessible from the host namespace.

## Repository Structure

```
ztunnel-under-the-hood/
├── cni-emulator/            # CNI node agent simulation
│   ├── cni-emulator.go      # Opens netns file and sends FD via UDS
│   ├── go.mod
│   └── go.sum
├── ztunnel-emulator/        # Ztunnel proxy simulation  
│   ├── ztunnel-emulator.go  # Receives netns FD and creates listeners
│   ├── go.mod
│   └── go.sum
├── ABOUT.MD                 # Detailed technical explanation
└── README.md               # This file
```

## What This Demo Shows

**Goal:** Make a process ("ztunnel") create **listeners inside a pod's netns** after a **CNI-like sender** provides a **netns file descriptor (FD)** over a **Unix Domain Socket (UDS)**—just like Istio ambient mode describes.

**Mapping:**
- **`cni-emulator`** ⇢ the **istio-cni node agent**: opens the pod's netns file (e.g., `/var/run/netns/ns2`) and **sends that FD** to ztunnel via UDS using `SCM_RIGHTS`.
- **`ztunnel-emulator`** ⇢ the **ztunnel proxy**: **receives the netns FD**, temporarily **`setns()` into that netns**, **binds listeners** on `127.0.0.1:{15008,15006,15001}` inside the pod netns, then returns to its own netns. The sockets remain attached to the **pod netns**.

## Components

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

## Test

### Prerequisites

- Linux system with network namespace support
- Go 1.24+
- Root privileges or appropriate capabilities


## Expected Output

When successful, you should see:

1. **Ztunnel output**: `listeners ready`
2. **Socket listing**: Shows listeners on ports 15008, 15006, 15001 in the pod namespace
3. **Test connection**: Returns "ok" when connecting to any of the ports


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


## References

- [Istio Ambient Traffic Redirection](https://istio.io/latest/docs/ambient/architecture/traffic-redirection/)
- [Istio CNI-Ztunnel Communication](https://istio.io/latest/docs/ambient/architecture/traffic-redirection/#:~:text=Once%20the%20istio,node%2Dlocal%20ztunnel)
- [Unix Domain Sockets and File Descriptor Passing](https://man7.org/linux/man-pages/man7/unix.7.html)
- [Network Namespaces](https://man7.org/linux/man-pages/man7/namespaces.7.html)
