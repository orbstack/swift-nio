#!/usr/bin/env python3

import xml.etree.ElementTree as ET
import sys

with open(sys.argv[1], 'r') as file:
    xml_str = file.read()

root = ET.fromstring(xml_str)
namespace = {'sparkle': 'http://www.andymatuschak.org/xml-namespaces/sparkle'}

# Hard-coded values
description_text = ""
release_notes_link_text = "https://cdn-updates.orbstack.dev/release-notes.html"

for item in root.findall('.//item'):
    # Check if the item already has these tags, if not add them
    if item.find('description') is None:
        description = ET.SubElement(item, 'description')
        description.text = description_text

    if item.find('sparkle:releaseNotesLink', namespace) is None:
        release_notes_link = ET.SubElement(item, '{http://www.andymatuschak.org/xml-namespaces/sparkle}releaseNotesLink')
        release_notes_link.text = release_notes_link_text

ET.register_namespace('sparkle', "http://www.andymatuschak.org/xml-namespaces/sparkle")

# Convert the updated XML back to a string
new_xml_str = ET.tostring(root, encoding="utf-8").decode("utf-8")

# write out
with open(sys.argv[1], 'w+') as file:
    file.write(new_xml_str)
