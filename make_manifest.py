import json
import os
import sys
import shutil


def main():
    if len(sys.argv) < 3:
        print("Usage: python make_manifest.py <index.json> <manifest.json>")
        exit()

    with open(sys.argv[1], "r") as f:
        index = json.load(f)
    with open(sys.argv[2], "r") as f:
        manifest = json.load(f)

    out = {
        "schemaVersion": 2,
        "mediaType": "application/vnd.oci.image.manifest.v1+json",
        "layers": [],
    }

    hash = index["manifests"][0]["digest"].split(":")[1]

    shutil.copyfile("blobs/sha256/" + hash, "oci.image.manifest.json")

    # meta = index["manifests"][0]
    # out["config"] = {
    #     "mediaType": "application/vnd.oci.image.config.v1+json",
    #     "digest": meta["digest"],
    #     "size": meta["size"],
    # }
    # out["annotations"] = meta["annotations"]

    # for layer in manifest[0]["Layers"]:
    #     sha256 = layer.split("blobs/sha256/")[1]
    #     out["layers"].append(manifest[0]["LayerSources"][f"sha256:{sha256}"])

    # with open("oci.image.manifest.json", "w") as f:
    #     json.dump(out, f)


if __name__ == "__main__":
    main()
