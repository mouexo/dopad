//go:build windows && !vigemdll

package main

// لا DLL مضمّنة: نبحث عنها بجانب DOpad.exe (انظر loadViGEmDLL).
var embeddedViGEm []byte
