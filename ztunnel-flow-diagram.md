# Ztunnel CNI Handoff Flow Diagram

## Mermaid Diagram Code


## Alternative: Sequence Diagram

```mermaid
sequenceDiagram
    participant CNI as CNI Agent<br/>(cni-emulator)
    participant ZT as Ztunnel<br/>(ztunnel-emulator)
    participant NS as Pod Namespace<br/>(ns2)

    CNI->>CNI: 1a. CNI gets an event that pod <br/>with namespace ns2 is created
    CNI->>CNI: 1b. CNI opens namespace file /var/run/netns/ns2 <br/>and get's its FD (file descriptor)
    CNI->>ZT: 2. Send FD via UDS to ztunnel
    ZT->>NS: 3. ZT does setns() into ns2 using FD <br/> i.e. it enters POD's namespace
    ZT->>NS: 4. ZT creates listeners e.g<br/>127.0.0.1:15008,15006,15001 <br/> in pod's namespace
    ZT->>ZT: 5. Return to host NS
    Note over NS: Listeners stay bound<br/>to pod namespace
```

## Usage Instructions

1. Copy either diagram code above
2. Paste into [Mermaid Live Editor](https://mermaid.live/)
3. Export as PNG/SVG for your blog post
4. Or use in GitHub/GitLab markdown files directly
