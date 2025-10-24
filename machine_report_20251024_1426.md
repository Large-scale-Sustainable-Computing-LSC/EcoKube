# Machine and Environment Report
_Generated: 2025-10-24T14:26:28+02:00_  


## Host OS and Kernel


### Distribution info

\$\ lsb_release -a
Distributor ID:	Debian
Description:	Debian GNU/Linux 12 (bookworm)
Release:	12
Codename:	bookworm

### os-release

\$\ cat /etc/os-release
PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
VERSION_ID="12"
VERSION="12 (bookworm)"
VERSION_CODENAME=bookworm
ID=debian
HOME_URL="https://www.debian.org/"
SUPPORT_URL="https://www.debian.org/support"
BUG_REPORT_URL="https://bugs.debian.org/"

### Kernel

\$\ uname -a
Linux mc-a4 6.1.0-33-amd64 #1 SMP PREEMPT_DYNAMIC Debian 6.1.133-1 (2025-04-10) x86_64 GNU/Linux

## CPU


### CPU summary

\$\ lscpu
Architecture:                         x86_64
CPU op-mode(s):                       32-bit, 64-bit
Address sizes:                        46 bits physical, 48 bits virtual
Byte Order:                           Little Endian
CPU(s):                               8
On-line CPU(s) list:                  0-7
Vendor ID:                            GenuineIntel
Model name:                           Intel(R) Xeon(R) CPU E5-1620 v4 @ 3.50GHz
CPU family:                           6
Model:                                79
Thread(s) per core:                   2
Core(s) per socket:                   4
Socket(s):                            1
Stepping:                             1
CPU(s) scaling MHz:                   95%
CPU max MHz:                          3800.0000
CPU min MHz:                          1200.0000
BogoMIPS:                             7000.09
Flags:                                fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush dts acpi mmx fxsr sse sse2 ss ht tm pbe syscall nx pdpe1gb rdtscp lm constant_tsc arch_perfmon pebs bts rep_good nopl xtopology nonstop_tsc cpuid aperfmperf pni pclmulqdq dtes64 monitor ds_cpl vmx smx est tm2 ssse3 sdbg fma cx16 xtpr pdcm pcid dca sse4_1 sse4_2 x2apic movbe popcnt tsc_deadline_timer aes xsave avx f16c rdrand lahf_lm abm 3dnowprefetch cpuid_fault epb cat_l3 cdp_l3 invpcid_single pti intel_ppin ssbd ibrs ibpb stibp tpr_shadow vnmi flexpriority ept vpid ept_ad fsgsbase tsc_adjust bmi1 hle avx2 smep bmi2 erms invpcid rtm cqm rdt_a rdseed adx smap intel_pt xsaveopt cqm_llc cqm_occup_llc cqm_mbm_total cqm_mbm_local dtherm ida arat pln pts md_clear flush_l1d
Virtualization:                       VT-x
L1d cache:                            128 KiB (4 instances)
L1i cache:                            128 KiB (4 instances)
L2 cache:                             1 MiB (4 instances)
L3 cache:                             10 MiB (1 instance)
NUMA node(s):                         1
NUMA node0 CPU(s):                    0-7
Vulnerability Gather data sampling:   Not affected
Vulnerability Itlb multihit:          KVM: Mitigation: VMX disabled
Vulnerability L1tf:                   Mitigation; PTE Inversion; VMX conditional cache flushes, SMT vulnerable
Vulnerability Mds:                    Mitigation; Clear CPU buffers; SMT vulnerable
Vulnerability Meltdown:               Mitigation; PTI
Vulnerability Mmio stale data:        Mitigation; Clear CPU buffers; SMT vulnerable
Vulnerability Reg file data sampling: Not affected
Vulnerability Retbleed:               Not affected
Vulnerability Spec rstack overflow:   Not affected
Vulnerability Spec store bypass:      Mitigation; Speculative Store Bypass disabled via prctl
Vulnerability Spectre v1:             Mitigation; usercopy/swapgs barriers and __user pointer sanitization
Vulnerability Spectre v2:             Mitigation; Retpolines; IBPB conditional; IBRS_FW; STIBP conditional; RSB filling; PBRSB-eIBRS Not affected; BHI Not affected
Vulnerability Srbds:                  Not affected
Vulnerability Tsx async abort:        Mitigation; Clear CPU buffers; SMT vulnerable

### CPU model (first entry)

\$\ grep -m1 "model name" /proc/cpuinfo
model name	: Intel(R) Xeon(R) CPU E5-1620 v4 @ 3.50GHz

## Memory


### Free memory

\$\ free -h
               total        used        free      shared  buff/cache   available
Mem:            62Gi       8.9Gi        38Gi        66Mi        15Gi        53Gi
Swap:          975Mi          0B       975Mi

### DIMM overview (requires sudo; best-effort)

\$\ sudo dmidecode -t memory | egrep -i "Size:|Type:|Speed:" | sed "/No Module Installed/d" | head -n 40
	Error Correction Type: Multi-bit ECC
	Size: 16 GB
	Type: DDR4
	Speed: 2400 MT/s
	Configured Memory Speed: 2400 MT/s
	Size: 16 GB
	Type: DDR4
	Speed: 2400 MT/s
	Configured Memory Speed: 2400 MT/s
	Size: 16 GB
	Type: DDR4
	Speed: 2400 MT/s
	Configured Memory Speed: 2400 MT/s
	Size: 16 GB
	Type: DDR4
	Speed: 2400 MT/s
	Configured Memory Speed: 2400 MT/s

## Storage


### Block devices

\$\ lsblk -o NAME,MODEL,SIZE,TYPE,MOUNTPOINT
NAME                  MODEL                SIZE TYPE MOUNTPOINT
sda                   ST2000NM0008-2F3100  1.8T disk 
├─sda1                                     512M part /boot/efi
├─sda2                                     488M part /boot
└─sda3                                     1.8T part 
  ├─mc--a4--vg-root                        1.8T lvm  /
  └─mc--a4--vg-swap_1                      976M lvm  [SWAP]
sr0                   Virtual CDROM        629M rom  

### Filesystem usage (root)

\$\ df -h /
Filesystem                   Size  Used Avail Use% Mounted on
/dev/mapper/mc--a4--vg-root  1.8T  209G  1.5T  12% /

## GPU


### PCI display devices

\$\ lspci | egrep -i "vga|3d|display"
08:00.0 VGA compatible controller: ASPEED Technology, Inc. ASPEED Graphics Family (rev 30)

### NVIDIA driver and GPUs

\$\ nvidia-smi
./get_host_properties.sh: line 17: nvidia-smi: command not found
(not available)

## Containers and Orchestration


### Docker

\$\ docker --version
Docker version 28.1.1, build 4eba377

### containerd

\$\ containerd --version
containerd containerd.io 1.7.27 05044ec0a9a75232cad458027ca83437aae3f4da

### kubectl (client)

\$\ kubectl version --client --output=yaml
clientVersion:
  buildDate: "2025-04-23T13:07:12Z"
  compiler: gc
  gitCommit: 60a317eadfcb839692a68eab88b2096f4d708f4f
  gitTreeState: clean
  gitVersion: v1.33.0
  goVersion: go1.24.2
  major: "1"
  minor: "33"
  platform: linux/amd64
kustomizeVersion: v5.6.0


### Helm

\$\ helm version --short
v3.17.3+ge4da497

## Toolchain


### Go

\$\ go version
go version go1.19.8 linux/amd64

### Python

\$\ python3 --version
Python 3.11.2

### Node.js

\$\ node --version
v22.18.0

### npm

\$\ npm --version
11.5.2

## Monitoring Stack


## Networking


### IP addresses

\$\ ip -brief address
lo               UNKNOWN        127.0.0.1/8 ::1/128 
enp2s0f0         UP             145.100.131.14/24 2001:610:2d0:200:ae1f:6bff:fe81:86f6/64 fe80::ae1f:6bff:fe81:86f6/64 
enp2s0f1         DOWN           
enp4s0f0np0      DOWN           
enp4s0f1np1      DOWN           
docker0          DOWN           172.17.0.1/16 fe80::bc37:2eff:fe13:c6fa/64 
br-006e27779643  DOWN           172.18.0.1/16 
br-3a35fe3ca84d  DOWN           192.168.33.1/24 
br-5fd4f83fc0bd  UP             172.21.0.1/16 fe80::806e:d9ff:fead:b698/64 
br-709726f6fbaf  DOWN           172.19.0.1/16 
br-73029f26ec2b  DOWN           192.168.49.1/24 
br-ce939023d8a4  UP             172.20.0.1/16 fc00:f853:ccd:e793::1/64 fe80::a041:ffff:feaf:92ed/64 
veth4406cc4@if2  UP             fe80::ccde:4cff:fea6:3866/64 
veth92b6b7e@if2  UP             fe80::844c:71ff:feb0:f876/64 
vethcac5cba@if2  UP             fe80::60bd:9fff:fe7b:3833/64 
veth691d14f@if2  UP             fe80::349d:7cff:fe26:a2e6/64 
vethb120c1e@if2  UP             fe80::2057:62ff:fe04:7712/64 
veth367f6ae@if2  UP             fe80::4411:9aff:fe6e:cda/64 
vethaeebcf9@if2  UP             fe80::84df:61ff:fe27:adad/64 
vethfc13d50@if2  UP             fe80::2097:b5ff:fe92:2505/64 
vetha84cb29@if2  UP             fe80::6033:61ff:fe72:6157/64 
veth4a62da6@if2  UP             fe80::e43e:78ff:fe72:bae0/64 
veth7bd5c23@if2  UP             fe80::2c44:70ff:fee6:cbe1/64 
vethf619252@if2  UP             fe80::a0a0:3eff:fe2f:8cd0/64 

### Routing table

\$\ ip route
default via 145.100.131.1 dev enp2s0f0 
145.100.131.0/24 dev enp2s0f0 proto kernel scope link src 145.100.131.14 
172.17.0.0/16 dev docker0 proto kernel scope link src 172.17.0.1 linkdown 
172.18.0.0/16 dev br-006e27779643 proto kernel scope link src 172.18.0.1 linkdown 
172.19.0.0/16 dev br-709726f6fbaf proto kernel scope link src 172.19.0.1 linkdown 
172.20.0.0/16 dev br-ce939023d8a4 proto kernel scope link src 172.20.0.1 
172.21.0.0/16 dev br-5fd4f83fc0bd proto kernel scope link src 172.21.0.1 
192.168.33.0/24 dev br-3a35fe3ca84d proto kernel scope link src 192.168.33.1 linkdown 
192.168.49.0/24 dev br-73029f26ec2b proto kernel scope link src 192.168.49.1 linkdown 

## Experiment Environment Notes (to fill if applicable)

