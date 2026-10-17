// Package opskit contains the embedded configuration template distributed with the CLI.
package opskit

import _ "embed"

// ConfigTemplate is the editable sample configuration, without credentials.
//
//go:embed config.example.yaml
var ConfigTemplate []byte

// KSInstaller is the audited upstream KubeSphere 3.4.1 installer manifest.
//
//go:embed assets/ks-installer-v3.4.1.yaml
var KSInstaller []byte

// KSConfiguration is the minimal component template; secrets are injected at runtime.
//
//go:embed assets/ks-config-v3.4.1.yaml
var KSConfiguration []byte
