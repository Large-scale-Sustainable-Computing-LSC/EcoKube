from fabrictestbed_extensions.fablib.fablib import FablibManager

# script = """
# #!/bin/bash
# hostname
# echo "Installing tools"
# sudo apt update -y
# sudo apt install -y git htop
# echo "Everything installed, script tested successfully. :)"
# """

try:
     fablib = FablibManager()
#     fablib.show_config()
     slice = fablib.get_slice("20250717_test_04")
     # slice.list_nodes()
     node = slice.get_node("utah-01")
     # node_remote = slice.add_node("test_remote_01")
     # stdout, stderr = node_remote.execute("echo Hello FABRIC!")
     # print(stdout)
     
     # print("The SSH command is: " + node.get_ssh_command())
     
     # stdout, stderr = node.execute("echo \"oi\"")
     
     # for node in slice.get_nodes():
     #      print("oi", node.get_name())
     for ns in slice.get_networks():
          print("network: ", f"Name: {ns.get_name()}, Type: {ns.get_type()}, Layer: {ns.get_layer()}")
    
     # stdout, stderr = node.execute(script)
     # print("Output:", stdout)
     # print("Errors:", stderr)

except Exception as e:
     print(f"Exception: {e}")