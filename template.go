// Package opskit contains the embedded configuration template distributed with the CLI.
package opskit

import _ "embed"

// ConfigTemplate is the editable sample configuration, without credentials.
//
//go:embed config.example.yaml
var ConfigTemplate []byte
