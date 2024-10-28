#!/usr/bin/env python3
import json
import sys
import subprocess
import os.path
import pathlib
import urllib.request
import configparser

# ==== constants ====

APP_NAMES = ['orbstack', 'orbstack helper']
SENTRY_FILE_URL = 'https://kdrag0n.sentry.io/api/0/projects/kdrag0n/orbstack/files/dsyms/'

# ==== utility ====

def json_loads_with_trailing(string):
    return json.JSONDecoder().raw_decode(string)

# ==== parsing and symbolication ====

def get_full_report_info(report):
    full_report = report.partition('-----------\nFull Report\n-----------')[2]
    full_report_json1_string = full_report[full_report.index('{'):]
    full_report_json1, full_report_json1_consumed = json_loads_with_trailing(full_report_json1_string)
    full_report_after_json1 = full_report_json1_string[full_report_json1_consumed:]
    full_report_json2_string = full_report_after_json1[full_report_after_json1.index('{'):]
    full_report_json2, _ = json_loads_with_trailing(full_report_json2_string)
    return full_report_json1, full_report_json2

def get_symbol(dwarf, load_addr, addr):
    process = subprocess.run(['atos', '-arch', 'arm64', '-o', dwarf, '-l', f'0x{load_addr:x}', f'0x{addr:x}'], capture_output=True, text=True)
    return process.stdout.rstrip('\n')

def symbolicate_report(app_names, dwarf, report):
    lower_app_names = [x.lower() for x in app_names]

    report_app_info, report_details = get_full_report_info(report)

    image_base = next((x for x in report_details['usedImages'] if x['name'].lower() in lower_app_names), None)

    report_lines = report.split('\n')
    for i, line in enumerate(report_lines):
        elements = line.split()
        if len(elements) < 3: continue
        if not elements[0].isdigit() or elements[1].lower() not in lower_app_names: continue

        addr_str = elements[2]
        addr = int(addr_str, 16)
        symbol = get_symbol(dwarf, image_base['base'], addr)
        line = line.replace(addr_str, f'{addr_str} {symbol} |')
        report_lines[i] = line

    return '\n'.join(report_lines)

# ==== sentry ====

def sentrycli_get_apikey():
    with open(os.path.join(pathlib.Path.home(), '.sentryclirc'), 'r') as sentry_file:
        parser = configparser.ConfigParser()
        parser.read_file(sentry_file)
        return parser['auth']['token']

def sentry_get_dsyms(apikey, url):
    with urllib.request.urlopen(
        urllib.request.Request(
            url,
            headers={'Authorization': f'Bearer {apikey}'}
        )
    ) as res:
        return json.loads(res.read())

def sentry_get_build_dsym(apikey, url, uuid):
    dsyms = sentry_get_dsyms(apikey, url)
    return next((x for x in dsyms if x['uuid'] == uuid), None)

# ==== main ====

def main():
    if len(sys.argv) < 2:
        print('usage: symbolicate_crash_report.py <filename> [dwarf]')
        return
    filename = sys.argv[1]

    with open(filename, 'r') as report_file:
        report = report_file.read()
    report_app_info, report_details = get_full_report_info(report)

    if len(sys.argv) < 3:
        apikey = sentrycli_get_apikey()
        build_uuid = report_app_info['slice_uuid']
        dsym = sentry_get_build_dsym(apikey, SENTRY_FILE_URL, build_uuid)
        url = f'{SENTRY_FILE_URL}?id={dsym['id']}'
        print(url)
        return
    else:
        dwarf = sys.argv[2]

    print(symbolicate_report(APP_NAMES, dwarf, report))

if __name__ == '__main__':
    main()
