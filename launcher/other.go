//go:build !windows

package main

import "errors"

// The launcher is a Windows program, like the game. On other systems it builds only for its tests, and starts
// nothing.

var errWindows = errors.New("the launcher runs on Windows")

func register(exe string) error { return errWindows }

func unregister() error { return errWindows }

func removeLater(exe, dir string) error { return errWindows }

func steamCopies(*title) []string { return nil }

func steamInstalled() bool { return false }

func startProgram(exe, commandLine, dir string) (game, error) { return nil, errWindows }

func presses(id string) (join, solo <-chan struct{}) { return nil, nil }

func press(id string, solo bool) {}

func rerun(exe, link string) error { return errWindows }

func single(id string) (release func(), ok bool) { return func() {}, true }

func mark() {}

func launchers() bool { return false }

func filesTurn(waits func()) (release func()) { return func() {} }

func tryFilesTurn() (release func(), ok bool) { return func() {}, true }

func interactive() bool { return false }

func windowless() bool { return false }

func showTray(host string, menu func() []menuItem) (stop func()) { return func() {} }

func pickFolder(title string) (string, error) { return "", errWindows }

func taskDialogs() func(question string, answers []string) int { return nil }

func openPage(url string) {}

func box(text string, bad bool) {}

func boxAsk(question string) bool { return false }

func freeBytes(dir string) (uint64, bool) { return 0, false }
