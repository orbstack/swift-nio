#!/usr/bin/env python3

import collections
import json
import csv
import sys

packages_by_cmd = collections.defaultdict(list)

# TEST_PKGS = ['yes', 'objdump', 'node', 'python', 'agda', 'java', 'opt']
# TEST_PKGS = ['unpack200', 'serialver', 'rmiregistry', 'rmid', 'rmic', 'pack200', 'keytool', 'jstatd', 'jstat', 'jstack', 'jshell', 'jrunscript', 'jps', 'jmod', 'jmap', 'jjs', 'jinfo', 'jimage', 'jhsdb', 'jfr', 'jdeps', 'jdeprscan', 'jdb', 'jcmd', 'javap', 'javadoc', 'javac', 'java', 'jarsigner', 'jar', 'jaotc', 'xjc', 'wsimport', 'wsgen', 'tnameserv', 'servertool', 'policytool', 'orbd', 'native2ascii', 'jsadebugd', 'jhat', 'javah', 'java-rmi.cgi', 'idlj', 'hsdb', 'extcheck', 'clhsdb', 'appletviewer', 'jwebserver']
# TEST_PKGS = ['vtkWrapPythonInit', 'vtkWrapPython', 'vtkWrapJava', 'vtkWrapHierarchy', 'vtkProbeOpenGLVersion', 'vtkParseJava', 'vtkpython']
TEST_PKGS = sys.argv[2:]

if TEST_PKGS:
    from openai import OpenAI

    client = OpenAI()


with open(sys.argv[1], "r") as f:
    reader = csv.reader(f)
    for cmd, package in reader:
        # exclude shell scripts
        if cmd.endswith(".sh"):
            continue

        packages_by_cmd[cmd].append(package)

with open("batch.jsonl", "w+") as f:
    for cmd, packages in packages_by_cmd.items():
        # single-package cases are easy
        if len(packages) == 1:
            continue
        # if multiple pkgs but have exact match, that's easy too
        if any(p == cmd for p in packages):
            continue

        if TEST_PKGS:
            if cmd in TEST_PKGS:
                print()
                print()
            else:
                continue

        # sort by alpha first
        packages.sort()
        # then stable sort by length (shortest first)
        packages.sort(key=lambda p: len(p))

        print(f'{cmd}:  {" ".join(packages)}')
        packages_joined = "\n".join(packages)

        prompt = f"""These NixOS packages provide "{cmd}".

Which package will the user probably install?
- Prefer un-versioned.
- Prefer new versions.
- Prefer no namespace.
- Prefer no "full" suffix.
- Prefer common tools.
- Prefer Zulu over OpenJDK.
- Prefer full implementations over busybox, toybox, etc."""

        req = {
            "custom_id": cmd,
            "method": "POST",
            "url": "/v1/chat/completions",
            "body": {
                "model": "gpt-3.5-turbo-0125",
                "messages": [
                    {
                        "role": "user",
                        "content": prompt,
                    }
                ],
                "temperature": 0,
                "max_tokens": 50,
                "tools": [
                    {
                        "type": "function",
                        "function": {
                            "name": "install_package",
                            "description": "Install package",
                            "parameters": {
                                "type": "object",
                                "properties": {
                                    "reason": {
                                        "type": "string",
                                        "description": "Briefly explain why this package is the best choice.",
                                    },
                                    "package": {"type": "string", "enum": packages},
                                },
                                "required": [
                                    "reason",
                                    "package",
                                ],
                            },
                        },
                    }
                ],
                "tool_choice": {
                    "type": "function",
                    "function": {"name": "install_package"},
                },
            },
        }
        f.write(json.dumps(req) + "\n")

        if TEST_PKGS:
            # actually make the request
            response = client.chat.completions.create(**req["body"])
            resp_json = response.choices[0].message.tool_calls[0].function.arguments
            resp = json.loads(resp_json)
            print(resp)
