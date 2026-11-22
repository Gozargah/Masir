package main

import "github.com/Gozargah/Masir/libs/masir-core/service"

// selinuxEnforcing reports whether SELinux is on and enforcing here; the dial-denial
// advice keys off it, and tests swap it.
var selinuxEnforcing = service.SelinuxEnforcing

// labelServiceBinary relabels the system service's copy of masir; only Linux has
// SELinux, and everywhere else the install is already correct as written.
var labelServiceBinary = service.EnsureBinaryLabel

// serviceLabelOK reports whether the service's binary already carries the label it
// needs, so an update whose content matches can tell "done" from "done but the
// label an earlier update lost".
var serviceLabelOK = service.LabelOK
