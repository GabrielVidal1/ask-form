#!/usr/bin/env node
// ask-form's npm launcher: runs the Go binary that this platform's package
// (ask-form-<os>-<arch>, an optional dependency) carries, with the same
// arguments, stdio and exit code.
'use strict'

const { spawnSync } = require('child_process')
const fs = require('fs')

function fail(msg) {
  process.stderr.write('ask-form: ' + msg + '\n')
  process.exit(1)
}

const os = { linux: 'linux', darwin: 'darwin' }[process.platform]
const cpu = { x64: 'x64', arm64: 'arm64' }[process.arch]
if (!os || !cpu) {
  fail(
    `no build for ${process.platform}/${process.arch}. With Go 1.22+: ` +
      'go install github.com/GabrielVidal1/ask-form/cmd/ask-form@latest',
  )
}
const pkg = `ask-form-${os}-${cpu}`

let bin
try {
  bin = require.resolve(`${pkg}/bin/ask-form`)
} catch {
  fail(`${pkg} isn't installed (npm left out the optional dependency?): npm install ${pkg}`)
}
try {
  fs.accessSync(bin, fs.constants.X_OK)
} catch {
  try {
    fs.chmodSync(bin, 0o755)
  } catch {
    // Read-only install: spawning reports it below.
  }
}

const r = spawnSync(bin, process.argv.slice(2), { stdio: 'inherit' })
if (r.error) fail(r.error.message)
if (r.signal) process.kill(process.pid, r.signal)
process.exit(r.status === null ? 1 : r.status)
