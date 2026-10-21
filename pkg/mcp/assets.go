package mcp

import _ "embed"

// NodeRuntime is the reproducible official JavaScript SDK bundle. It is copied
// into digest-pinned Runtime tool snapshots or a supervised local plugin's private
// support directory, never loaded from the network during an invocation.
//
//go:embed assets/node-runtime.mjs
var NodeRuntime string

// NodeAdapter is Pot's flat tool adapter, independent of the Control catalogue.
//
//go:embed node/adapter.mjs
var NodeAdapter string
