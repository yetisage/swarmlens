#!/usr/bin/env python3
"""Records the API responses the tests in internal/api run against.

    python3 scripts/record-fixtures.py internal/api/testdata/docker-29.9.2

It talks to the daemon at $DOCKER_SOCK (default /var/run/docker.sock) with curl,
through the API version the swarmlens client negotiates (1.51), and writes one
JSON file per response. The swarm has to hold what the tests expect: see
internal/api/testdata/docker-29.9.2/README.md for the commands.

Only what the tests need is kept: the nodes' TLS blobs are dropped, /info is
reduced to the fields swarmlens reads, and the daemon's data root is replaced by
/var/lib/docker so that nothing about the machine that recorded them is left.
"""
import json
import os
import subprocess
import sys

SOCK = os.environ.get("DOCKER_SOCK", "/var/run/docker.sock")
API = "/v1.51"

if len(sys.argv) != 2:
    sys.exit(__doc__)
OUT = sys.argv[1]
os.makedirs(OUT, exist_ok=True)


def get(path):
    raw = subprocess.run(
        ["curl", "-sS", "--fail", "--unix-socket", SOCK, "http://localhost" + API + path],
        check=True, capture_output=True,
    ).stdout
    return json.loads(raw)


def save(name, data, scrub=None):
    text = json.dumps(data, indent=2, sort_keys=True) + "\n"
    if scrub:
        text = text.replace(scrub, "/var/lib/docker")
    with open(os.path.join(OUT, name), "w") as f:
        f.write(text)


info = get("/info")
data_root = info["DockerRootDir"]

services = get("/services?status=true")
save("services.json", services)
save("service.json", get("/services/shop_api"))

tasks = get("/tasks")
save("tasks.json", tasks)
by = {}
for t in tasks:
    name = next((s["Spec"]["Name"] for s in services if s["ID"] == t["ServiceID"]), "?")
    by.setdefault((name, t["Status"]["State"]), t)
save("task.json", get("/tasks/" + by[("shop_api", "running")]["ID"]))
save("task-failed.json", get("/tasks/" + by[("broken", "rejected")]["ID"]))


def slim(node):
    node.get("Description", {}).pop("TLSInfo", None)
    return node


nodes = [slim(n) for n in get("/nodes")]
save("nodes.json", nodes)
save("node.json", slim(get("/nodes/" + nodes[0]["ID"])))

save("networks.json", get("/networks"))
save("network.json", get("/networks/shop_back"))
save("volumes.json", get("/volumes"), scrub=data_root)
save("volume.json", get("/volumes/shop_data"), scrub=data_root)

secrets = get("/secrets")
save("secrets.json", secrets)
save("secret.json", get("/secrets/" + secrets[0]["ID"]))
configs = get("/configs")
save("configs.json", configs)
save("config.json", get("/configs/" + configs[0]["ID"]))

swarm = info["Swarm"]
save("info.json", {
    "Name": info["Name"],
    "ServerVersion": info["ServerVersion"],
    "Swarm": {k: swarm[k] for k in ("NodeID", "NodeAddr", "LocalNodeState", "ControlAvailable", "Nodes", "Managers")}
    | {"Cluster": {"ID": swarm["Cluster"]["ID"], "Spec": {"Name": swarm["Cluster"]["Spec"]["Name"]}}},
})
save("version.json", {"Version": info["ServerVersion"], "ApiVersion": "1.51", "MinAPIVersion": "1.24"})
print("recorded", len(os.listdir(OUT)), "files in", OUT)
