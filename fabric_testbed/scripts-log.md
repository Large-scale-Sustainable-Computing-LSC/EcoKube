Following the tutorials here:
- https://learn.fabric-testbed.net/knowledge-base/generating-ssh-configuration-and-ssh-keys/
- Original page: https://learn.fabric-testbed.net/knowledge-base/logging-into-fabric-vms/

1) Create the keypair
ssh-keygen -t rsa -b 4096 -f ~/.ssh/id_rsa_fabric -C "FABRIC_key"

2) Fix perms (SSH is picky)
```bash
chmod 600 ~/.ssh/id_rsa_fabric
chmod 644 ~/.ssh/id_rsa_fabric.pub

chmod 600 ~/.ssh/id_rsa_sliver
chmod 644 ~/.ssh/id_rsa_sliver.pub
```

3) Create the `~/.ssh/fabric_ssh_config`, and put:
```txt
UserKnownHostsFile /dev/null
StrictHostKeyChecking no
ServerAliveInterval 120 

Host bastion.fabric-testbed.net
     User goncalojferreira92_0000346771
     ForwardAgent yes
     Hostname %h
     IdentityFile ~/.ssh/id_rsa_fabric
     IdentitiesOnly yes

Host * !bastion.fabric-testbed.net
     ProxyJump goncalojferreira92_0000346771@bastion.fabric-testbed.net:22
     IdentityFile ~/.ssh/id_rsa_sliver
     IdentitiesOnly yes
```

> Note: I did not understand this before, but we have two-step authentication here.
> First, we log in through the Bastion; THEN, we "jump-in" to the host. Both require different SSH bonding.

4) Finally, log-in:
ssh -F ~/.ssh/fabric_ssh_config -i ~/.ssh/id_rsa_fabric <username_os>@<ipv6_node_ip>
ssh -F ~/.ssh/fabric_ssh_config <username_os>@<ipv6_node_ip>

> Finally, this one is the one that works :clap:
ssh -F ~/.ssh/fabric_ssh_config -i ~/.ssh/id_rsa_sliver ubuntu@2001:1948:417:7:f816:3eff:fe8c:f8d5


5) Try running the script in Python
Make sure that all the environment variables are present (such as `~/.fabric/tokens.json`).
