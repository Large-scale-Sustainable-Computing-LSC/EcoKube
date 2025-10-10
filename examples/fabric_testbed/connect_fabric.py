# import os
# from fabrictestbed_extensions.fablib.fablib import FablibManager

# os.environ["FABRIC_BASTION_USERNAME"] = "goncalojferreira92_0000346771"
# os.environ["FABRIC_BASTION_KEY"] = os.path.expanduser("~/.ssh/id_rsa_fabric")
# os.environ["FABRIC_SLICE_PRIVATE_KEY_FILE"] = os.path.expanduser("~/.ssh/id_rsa_sliver")
# os.environ["FABRIC_SLICE_PUBLIC_KEY_FILE"] = os.path.expanduser("~/.ssh/id_rsa_sliver.pub")
# os.environ["FABRIC_TOKEN_FILE"] = os.path.expanduser("~/.fabric/tokens.json")

# fab = FablibManager()
# print("logged into the FabLibAPI!")
# # slice = fab.get_slice(name="MC-Test Ipv4")
# # node = slice.get_nodes()[0]
# # print(node.execute("whoami && hostname").stdout)


from fabrictestbed_extensions.fablib.fablib import FablibManager
fab = FablibManager()
# print([s.get_name() for s in fab.list_slices()])

