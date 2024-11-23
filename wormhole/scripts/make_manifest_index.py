# creates a multi-arch manifest index from the corresponding amd64 and arm64 docker indices

import json
import sys

if len(sys.argv) != 3:
    print("usage: make_manifest_index.py <amd64_index_file> <arm64_index_file>")
    sys.exit(1)

amd64_file = json.loads(open(sys.argv[1], "r").read())
arm64_file = json.loads(open(sys.argv[2], "r").read())

assert len(amd64_file["manifests"]) == len(arm64_file["manifests"]) == 1, "expected one manifest per architecture"

amd64_manifest = amd64_file["manifests"][0]
arm64_manifest = arm64_file["manifests"][0]

amd64_manifest["platform"] = {"architecture": "amd64", "os": "linux"}
arm64_manifest["platform"] = {"architecture": "arm64", "os": "linux"}

del arm64_manifest["annotations"]
del amd64_manifest["annotations"]

manifests = [amd64_manifest, arm64_manifest]
out = {
    "schemaVersion": 2,
    "mediaType": "application/vnd.oci.image.index.v1+json",
    "manifests": manifests
}

print(json.dumps(out))
