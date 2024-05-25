import plistlib
import sys

catalog = plistlib.loads(sys.stdin.buffer.read())

src_version_prefixes = [
    # 13.x
    '22',
    # 14.x
    '23',
]
# macOS 14 beta 6
target_version = '23A344'

for product in catalog['Products'].values():
    mac_build_version = product['ExtendedMetaInfo']['BuildVersion']
    pkg_url = product['Packages'][0]['URL']
    if mac_build_version == target_version:
        with open('target', 'w+') as f:
            f.write(pkg_url)
    elif any(mac_build_version.startswith(prefix) for prefix in src_version_prefixes):
        print(pkg_url)
