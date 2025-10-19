import yaml

configmap_file = Path("../manifest/templates/centralunit-configmap.yaml")
centralunit_configmap = yaml.safe_load(configmap_file.read_text())

eu_cluster_configmap_path = Path("/mnt/data/eu-cluster-configmap.yaml")
eu_cluster_yaml = yaml.safe_load(eu_cluster_configmap_path.read_text())
clusters_data = json.loads(eu_cluster_yaml['data']['clusters.json'])

config = {
    "clusters": []
}

for cluster in clusters_data:
    node_name = cluster["name"].replace("cluster", "computenode")
    config["clusters"].append({
        "name": node_name,
        "region": cluster["region"],
        "location": cluster["location"],
        "cpu_capacity": cluster["cpu_capacity"],
        "energy_bias": cluster["energy_bias"],
        "carbon_intensity": cluster["carbon_intensity"],
        "latitude": cluster["latitude"],
        "longitude": cluster["longitude"]
    })

# Convert to indented JSON for inclusion
config_json = json.dumps(config["clusters"], indent=2)
generated_configmap_yaml = "\n".join([
    "apiVersion: v1",
    "kind: ConfigMap",
    "metadata:",
    "  name: centralunit-config",
    "  namespace: eu-central",
    "data:",
    "  config.json: |"
])
generated_configmap_yaml += "\n" + "\n".join(f"    {line}" for line in config_json.splitlines())

# Save to file
output_path = Path("../manifest/templates/centralunit-updated-configmap.yaml")
output_path.write_text(generated_configmap_yaml.strip())

output_path.name
