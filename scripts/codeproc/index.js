// ES6 version using asynchronous iterators, compatible with node v10.0+

import { promises as fsp } from "fs"
import fs from "fs"
import { join } from "path"
import TreeSitterGo from "tree-sitter-go"
import TreeSitterRust from "tree-sitter-rust"
import TreeSitterPython from "tree-sitter-python"
import TreeSitterBash from "tree-sitter-bash"
import TreeSitterC from "tree-sitter-c"
import TreeSitterCpp from "tree-sitter-cpp"
import TreeSitterSwift from "tree-sitter-swift"
import TreeSitterHtml from "tree-sitter-html"
import TreeSitterToml from "tree-sitter-toml"
import TreeSitterYaml from "tree-sitter-yaml"
import Parser from "tree-sitter"

const parserPkgs = {
    '.go': TreeSitterGo,
    '.rs': TreeSitterRust,
    '.py': TreeSitterPython,
    '.sh': TreeSitterBash,
    '.c': TreeSitterC,
    '.cpp': TreeSitterCpp,
    '.h': TreeSitterC,
    '.swift': TreeSitterSwift,
    '.html': TreeSitterHtml,
    // XML close enough
    //'.plist': TreeSitterHtml, // needs testing
    //'.xml': TreeSitterHtml,
    '.toml': TreeSitterToml,
    '.yml': TreeSitterYaml,
    '.yaml': TreeSitterYaml,
}

const includeComments = [
    '//go:', // go:embed, go:generate, go:build
    '//export', // Cgo export
    '#include', // Cgo C block
    '// swift-tools', // Package.swift
    '# syntax:', // dockerfile
    '#!/', // shebang
    'Copyright', // legal
    'License:', // legal, e.g. bnat
    'SPDX-License-Identifier', // legal
]

const skipFiles = [
    '/config.sh', // important comments for user
]

function newParser(pkg) {
    let parser = new Parser()
    parser.setLanguage(pkg)
    return parser
}

const parsers = Object.fromEntries(Object.entries(parserPkgs)
    .map(([suffix, pkg]) => [suffix, newParser(pkg)]))

async function* walk(dir) {
    for await (const d of await fsp.opendir(dir)) {
        const entry = join(dir, d.name)
        if (d.isDirectory()) yield* walk(entry)
        else if (d.isFile()) yield entry
    }
}

/** @param {Parser.TreeCursor} cursor */
function walkAndReplace(cursor, cb) {
    // visit every node and call cb on it
    // do current node first
    cb(cursor)

    if (cursor.gotoFirstChild()) {
        do {
            walkAndReplace(cursor, cb)
        } while(cursor.gotoNextSibling())
        cursor.gotoParent()
    }
}

let srcDir = process.argv[2]
let spaceAscii = 32
for await (let path of walk(srcDir)) {
    if (skipFiles.some((s) => path === srcDir + s)) {
        console.log("SKIP: " + path)
        continue
    }

    console.log(path)
    let str = await fsp.readFile(path, "utf8")

    let done = false
    for (let [suffix, parser] of Object.entries(parsers)) {
        if (path.endsWith(suffix)) {
            // to buffer
            let buffer = Buffer.from(str, 'utf16le')
            let tree = parser.parse(str)
            //console.log(tree.rootNode.toString())
            // walk the tree
            let cursor = tree.walk()
            walkAndReplace(cursor, /** @param {Parser.TreeCursor} cursor */ (cursor) => {
                // comment, block_comment, line_comment, multiline_comment
                if (cursor.nodeType.includes("comment")) {
                    if (includeComments.some((s) => cursor.nodeText.includes(s))) {
                        return // skip
                    }

                    // replace data with zeros in original string buffer
                    //rawData.fill(spaceAscii, cursor.startIndex, cursor.endIndex)
                    // just use the raw string
                    // these indices were converted from utf16 as (index = byte / 2)
                    // so need to use utf16 buffer to prevent unicode from throwing it out
                    for (let i = cursor.startIndex; i < cursor.endIndex; i++) {
                        //str[i] = spaceAscii
                        buffer[i*2] = spaceAscii
                        buffer[i*2+1] = 0
                    }
                }
            })

            // continue filtering with some regex
            str = buffer.toString('utf16le')
            //don't remove lines filled with whitespace, could break indented raw strings
            done = true
            break
        }
    }

    if (!done) {
        if (path.endsWith('/Dockerfile')) {
            // special case hack
            str = str.replace(/^\s*#.*$/gm, '')
        }

        console.log("ERROR: UNKNOWN FILE: " + path)
    }

    // write out
    await fsp.writeFile(path, str)
}
