#!/usr/bin/env python3
from json import JSONDecoder
from sys import argv
from subprocess import run

def json_loads_with_trailing(string):
    return JSONDecoder().raw_decode(string)

def get_full_report_info(report):
    full_report = report.partition('-----------\nFull Report\n-----------')[2]
    full_report_json1_string = full_report[full_report.index('{'):]
    full_report_json1, full_report_json1_consumed = json_loads_with_trailing(full_report_json1_string)
    full_report_after_json1 = full_report_json1_string[full_report_json1_consumed:]
    full_report_json2_string = full_report_after_json1[full_report_after_json1.index('{'):]
    full_report_json2, _ = json_loads_with_trailing(full_report_json2_string)
    return full_report_json1, full_report_json2

def get_symbol(dwarf, load_addr, addr):
    process = run(['atos', '-arch', 'arm64', '-o', dwarf, '-l', f'0x{load_addr:x}', f'0x{addr:x}'], capture_output=True, text=True)
    return process.stdout.rstrip('\n')

def main():
    if len(argv) < 3:
        print('usage: symbolicate_crash_report.py <dwarf> <filename>')
        return
    dwarf = argv[1]
    filename = argv[2]
    with open(filename, 'r') as fileobj:
        report = fileobj.read()
    report_short_info, report_long_info = get_full_report_info(report)

    def orbstack_filter(x):
        return x['name'] == 'OrbStack'
    orbstack_base = next(filter(orbstack_filter, report_long_info['usedImages']))

    report_lines = report.split('\n')
    for i, line in enumerate(report_lines):
        elements = line.split()
        if len(elements) < 3: continue
        if not elements[0].isdigit() or elements[1] != 'OrbStack': continue

        addr_str = elements[2]
        addr = int(addr_str, 16)
        symbol = get_symbol(dwarf, orbstack_base['base'], addr)
        line = line.replace(addr_str, f'{addr_str} {symbol} |')
        report_lines[i] = line

    print('\n'.join(report_lines))

if __name__ == '__main__':
    main()
