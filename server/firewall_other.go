//go:build !windows

package main

// ضبط جدار الحماية تلقائيًا خاص بويندوز (انظر firewall_windows.go).
func ensureFirewall()    {}
func fixFirewall() error { return nil }
