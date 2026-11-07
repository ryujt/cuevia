package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

var kernel = syscall.NewLazyDLL("kernel32.dll")

type shellExecuteInfo struct {
	Size, Mask             uint32
	Window                 uintptr
	Verb, File, Parameters *uint16
	Directory              *uint16
	Show                   int32
	Instance, IDList       uintptr
	Class                  *uint16
	ClassKey               uintptr
	Hotkey                 uint32
	IconOrMonitor, Process uintptr
}

func externalTarget(target string) (string, error) {
	if strings.ContainsAny(target, "\x00\r\n") {
		return "", fmt.Errorf("invalid external target")
	}
	if address, err := url.Parse(target); err == nil && address.Scheme == "http" && address.Hostname() == "127.0.0.1" && address.User == nil && address.Opaque == "" {
		port, err := strconv.Atoi(address.Port())
		if err == nil && port > 0 && port <= 65535 {
			return address.String(), nil
		}
	}
	program, err := os.Executable()
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(target)
	if err == nil && strings.EqualFold(filepath.Clean(path), filepath.Join(filepath.Dir(program), "INSTALL.txt")) {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", fmt.Errorf("only Cuevia's local web address or installed guide can be opened")
}

func shellOpen(target string) error {
	var inJob uint32
	ok, _, _ := kernel.NewProc("IsProcessInJob").Call(^uintptr(0), 0, uintptr(unsafe.Pointer(&inJob)))
	if ok == 0 || inJob != 0 {
		// Explicit breakaway can leave a restrictive ancestor job in place.
		return explorerOpen(target)
	}
	// Shell extensions can need a COM STA. Keep initialization, activation and
	// teardown on one OS thread, and wait for asynchronous shell work to finish
	// before this deliberately short-lived helper exits.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ole := syscall.NewLazyDLL("ole32.dll")
	status, _, _ := ole.NewProc("CoInitializeEx").Call(0, 0x2|0x4)
	if int32(status) < 0 {
		return explorerOpen(target)
	}
	defer ole.NewProc("CoUninitialize").Call()
	program := target
	var parameters *uint16
	if !strings.HasPrefix(target, "http://") {
		program = filepath.Join(os.Getenv("SystemRoot"), "System32", "notepad.exe")
		parameters, _ = syscall.UTF16PtrFromString(`"` + target + `"`)
	}
	file, err := syscall.UTF16PtrFromString(program)
	if err != nil {
		return err
	}
	verb, _ := syscall.UTF16PtrFromString("open")
	info := shellExecuteInfo{Mask: 0x100 | 0x400, Verb: verb, File: file, Parameters: parameters, Show: 1}
	info.Size = uint32(unsafe.Sizeof(info))
	result, _, callErr := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteExW").Call(uintptr(unsafe.Pointer(&info)))
	if result == 0 {
		return fmt.Errorf("open external viewer: %w", callErr)
	}
	return nil
}

// An already running Explorer desktop executes this operation outside the
// launcher job. Plain Shell.Application.ShellExecute alone can run in-process.
const explorerOpenScript = `$ErrorActionPreference='Stop'; [Console]::InputEncoding=[Text.UTF8Encoding]::new($false); $target=[Console]::In.ReadToEnd(); $shell=New-Object -ComObject Shell.Application; $location=[object]0; $window=0; $desktop=$shell.Windows().FindWindowSW([ref]$location,[ref]$location,8,[ref]$window,1); if (-not $desktop) { throw 'Windows Explorer desktop is unavailable.' }; if ($target.StartsWith('http://')) { $program=$target; $parameters='' } else { $program=Join-Path $env:SystemRoot 'System32\notepad.exe'; $parameters='"'+$target+'"' }; $desktop.Document.Application.ShellExecute($program,$parameters,'','open',1)`

func explorerOpen(target string) error {
	command := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-Command", explorerOpenScript)
	command.Stdin = strings.NewReader(target)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("open through Windows Explorer: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func openExternal(target string) error {
	validated, err := externalTarget(target)
	if err != nil {
		return err
	}
	program, err := os.Executable()
	if err != nil {
		return err
	}
	child := exec.Command(program, "--shell-open-external", validated)
	child.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x01000000 | 0x08000000}
	if err := child.Start(); err == nil {
		return child.Wait()
	}
	// A parent job (for example a managed terminal) may prohibit breaking away.
	// Fall back to the real Explorer broker rather than inheriting the browser.
	return explorerOpen(validated)
}

type basicLimits struct {
	ProcessTime, JobTime int64
	Flags                uint32
	Minimum, Maximum     uintptr
	ActiveProcesses      uint32
	Affinity             uintptr
	Priority, Scheduling uint32
}
type extendedLimits struct {
	Basic                                                      basicLimits
	IO                                                         [6]uint64
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

func run() error {
	program, err := os.Executable()
	if err != nil {
		return err
	}
	job, _, callErr := kernel.NewProc("CreateJobObjectW").Call(0, 0)
	if job == 0 {
		return callErr
	}
	defer syscall.CloseHandle(syscall.Handle(job))
	// External viewers may explicitly escape; ordinary backend descendants stay.
	limits := extendedLimits{Basic: basicLimits{Flags: 0x2000 | 0x0800}}
	ok, _, callErr := kernel.NewProc("SetInformationJobObject").Call(job, 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	if ok == 0 {
		return callErr
	}
	arguments := []string{"-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-File", filepath.Join(filepath.Dir(program), "Cuevia.ps1")}
	arguments = append(arguments, os.Args[1:]...)
	command := exec.Command(filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), arguments...)
	command.Dir = filepath.Dir(program)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if err := command.Start(); err != nil {
		return err
	}
	process, err := syscall.OpenProcess(0x0100|0x0001, false, uint32(command.Process.Pid))
	if err != nil {
		command.Process.Kill()
		command.Wait()
		return err
	}
	ok, _, callErr = kernel.NewProc("AssignProcessToJobObject").Call(job, uintptr(process))
	syscall.CloseHandle(process)
	if ok == 0 {
		command.Process.Kill()
		command.Wait()
		return fmt.Errorf("manage Cuevia process lifetime: %w", callErr)
	}
	// Closing the job handle stops the entire server tree after a forced exit.
	return command.Wait()
}
func main() {
	var err error
	switch {
	case len(os.Args) == 3 && os.Args[1] == "--open-external":
		err = openExternal(os.Args[2])
	case len(os.Args) == 3 && os.Args[1] == "--shell-open-external":
		var target string
		target, err = externalTarget(os.Args[2])
		if err == nil {
			err = shellOpen(target)
		}
	default:
		err = run()
	}
	if err != nil {
		if _, reported := err.(*exec.ExitError); reported {
			os.Exit(1)
		}
		message, _ := syscall.UTF16PtrFromString("Cuevia could not start: " + err.Error())
		title, _ := syscall.UTF16PtrFromString("Cuevia")
		syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
		os.Exit(1)
	}
}
