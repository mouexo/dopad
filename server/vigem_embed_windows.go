//go:build windows && vigemdll

package main

import _ "embed"

// يُضمَّن عند البناء بالوسم vigemdll (يضيفه make build-windows تلقائيًا إن وُجد server/ViGEmClient.dll).
//
//go:embed ViGEmClient.dll
var embeddedViGEm []byte
