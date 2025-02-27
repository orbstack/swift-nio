import plistlib
import sys

catalog = plistlib.loads(sys.stdin.buffer.read())

src_version_prefixes = [
    # 13.x
    "22",
    # 14.x
    "23",
    # 15.x
    "24",  # latest: 24A5279h
]
# macOS 15.4 beta 1
target_version = "24E5206s"

for product in catalog["Products"].values():
    mac_build_version = product["ExtendedMetaInfo"]["BuildVersion"]
    pkg_url = product["Packages"][0]["URL"]
    if mac_build_version == target_version:
        with open("target", "w+") as f:
            f.write(pkg_url)
    elif any(mac_build_version.startswith(prefix) for prefix in src_version_prefixes):
        print(pkg_url)
