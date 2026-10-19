//go:build windows

package main

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const (
	sfxTrailerSize = 48
	sfxMagic       = "NSMSFX01"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")

	procRegisterClassExW        = user32.NewProc("RegisterClassExW")
	procCreateWindowExW         = user32.NewProc("CreateWindowExW")
	procDefWindowProcW          = user32.NewProc("DefWindowProcW")
	procShowWindow              = user32.NewProc("ShowWindow")
	procUpdateWindow            = user32.NewProc("UpdateWindow")
	procGetMessageW             = user32.NewProc("GetMessageW")
	procTranslateMessage        = user32.NewProc("TranslateMessage")
	procDispatchMessageW        = user32.NewProc("DispatchMessageW")
	procPostQuitMessage         = user32.NewProc("PostQuitMessage")
	procPostMessageW            = user32.NewProc("PostMessageW")
	procInvalidateRect          = user32.NewProc("InvalidateRect")
	procBeginPaint              = user32.NewProc("BeginPaint")
	procEndPaint                = user32.NewProc("EndPaint")
	procFillRect                = user32.NewProc("FillRect")
	procDrawTextW               = user32.NewProc("DrawTextW")
	procGetSystemMetrics        = user32.NewProc("GetSystemMetrics")
	procLoadIconW               = user32.NewProc("LoadIconW")
	procLoadCursorW             = user32.NewProc("LoadCursorW")
	procMessageBoxW             = user32.NewProc("MessageBoxW")
	procCreateSolidBrush        = gdi32.NewProc("CreateSolidBrush")
	procDeleteObject            = gdi32.NewProc("DeleteObject")
	procSetBkMode               = gdi32.NewProc("SetBkMode")
	procSetTextColor            = gdi32.NewProc("SetTextColor")
	procCreateFontW             = gdi32.NewProc("CreateFontW")
	procSelectObject            = gdi32.NewProc("SelectObject")
	procGetModuleHandleW        = kernel32.NewProc("GetModuleHandleW")
	procSHGetSpecialFolderPathW = shell32.NewProc("SHGetSpecialFolderPathW")
	procCoInitializeEx          = ole32.NewProc("CoInitializeEx")
	procCoUninitialize          = ole32.NewProc("CoUninitialize")
	procCoCreateInstance        = ole32.NewProc("CoCreateInstance")
)

type wndClassExW struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance    uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type rect struct {
	Left, Top, Right, Bottom int32
}

type paintStruct struct {
	Hdc         uintptr
	Erase       int32
	RcPaint     rect
	Restore     int32
	IncUpdate   int32
	RgbReserved [32]byte
}

type point struct {
	X, Y int32
}

type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
}

type guid struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	clsidShellLink = guid{0x00021401, 0x0000, 0x0000, [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIShellLinkW = guid{0x000214F9, 0x0000, 0x0000, [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIPersistFile = guid{0x0000010B, 0x0000, 0x0000, [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
)

var (
	uiMu       sync.Mutex
	uiTitle    = "Installing NSM — Powered by AFX"
	uiSubtitle = "Preparing %LOCALAPPDATA%\\NSM\\NSM.app..."
	uiPct      = 0
	uiHwnd     uintptr
)

func setUIState(title, subtitle string, pct int) {
	uiMu.Lock()
	if title != "" {
		uiTitle = title
	}
	if subtitle != "" {
		uiSubtitle = subtitle
	}
	if pct < 0 {
		pct = 0
	} else if pct > 100 {
		pct = 100
	}
	uiPct = pct
	hwnd := uiHwnd
	uiMu.Unlock()
	if hwnd != 0 {
		procInvalidateRect.Call(hwnd, 0, 0)
	}
}

func rgb(r, g, b byte) uintptr {
	return uintptr(uint32(r) | (uint32(g) << 8) | (uint32(b) << 16))
}

func wndProc(hwnd uintptr, uMsg uint32, wParam, lParam uintptr) uintptr {
	const (
		WM_DESTROY = 0x0002
		WM_PAINT   = 0x000F
		WM_CLOSE   = 0x0010
	)
	switch uMsg {
	case WM_PAINT:
		var ps paintStruct
		hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		if hdc != 0 {
			uiMu.Lock()
			title := uiTitle
			subtitle := uiSubtitle
			pct := uiPct
			uiMu.Unlock()

			bgBrush, _, _ := procCreateSolidBrush.Call(rgb(11, 16, 27))
			cardBrush, _, _ := procCreateSolidBrush.Call(rgb(20, 29, 47))
			barBgBrush, _, _ := procCreateSolidBrush.Call(rgb(30, 42, 68))
			barFgBrush, _, _ := procCreateSolidBrush.Call(rgb(47, 123, 255))

			fullRc := rect{0, 0, 480, 185}
			procFillRect.Call(hdc, uintptr(unsafe.Pointer(&fullRc)), bgBrush)

			topAccent := rect{0, 0, 480, 4}
			procFillRect.Call(hdc, uintptr(unsafe.Pointer(&topAccent)), barFgBrush)

			barBg := rect{28, 118, 452, 134}
			procFillRect.Call(hdc, uintptr(unsafe.Pointer(&barBg)), barBgBrush)
			if pct > 0 {
				w := int32((424 * pct) / 100)
				if w < 4 {
					w = 4
				}
				barFg := rect{28, 118, 28 + w, 134}
				procFillRect.Call(hdc, uintptr(unsafe.Pointer(&barFg)), barFgBrush)
			}

			procSetBkMode.Call(hdc, 1) // TRANSPARENT
			fontName, _ := syscall.UTF16PtrFromString("Segoe UI")
			hFontTitle, _, _ := procCreateFontW.Call(
				uintptr(uint32(int32(-20))), 0, 0, 0, 700, 0, 0, 0, 1, 0, 0, 5, 0,
				uintptr(unsafe.Pointer(fontName)),
			)
			hFontSub, _, _ := procCreateFontW.Call(
				uintptr(uint32(int32(-14))), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0,
				uintptr(unsafe.Pointer(fontName)),
			)

			oldFont, _, _ := procSelectObject.Call(hdc, hFontTitle)
			procSetTextColor.Call(hdc, rgb(240, 246, 255))
			titleU16, _ := syscall.UTF16FromString(title)
			titleRc := rect{28, 26, 452, 60}
			procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&titleU16[0])), uintptr(len(titleU16)-1), uintptr(unsafe.Pointer(&titleRc)), 0x00000000)

			procSelectObject.Call(hdc, hFontSub)
			procSetTextColor.Call(hdc, rgb(156, 176, 209))
			subU16, _ := syscall.UTF16FromString(subtitle)
			subRc := rect{28, 66, 452, 102}
			procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&subU16[0])), uintptr(len(subU16)-1), uintptr(unsafe.Pointer(&subRc)), 0x00008000) // DT_END_ELLIPSIS

			pctStr := fmt.Sprintf("%d%%", pct)
			pctU16, _ := syscall.UTF16FromString(pctStr)
			pctRc := rect{28, 142, 452, 168}
			procSetTextColor.Call(hdc, rgb(96, 165, 250))
			procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&pctU16[0])), uintptr(len(pctU16)-1), uintptr(unsafe.Pointer(&pctRc)), 0x00000002) // DT_RIGHT

			procSelectObject.Call(hdc, oldFont)
			procDeleteObject.Call(hFontTitle)
			procDeleteObject.Call(hFontSub)
			procDeleteObject.Call(bgBrush)
			procDeleteObject.Call(cardBrush)
			procDeleteObject.Call(barBgBrush)
			procDeleteObject.Call(barFgBrush)
			procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		}
		return 0
	case WM_DESTROY:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(uMsg), wParam, lParam)
	return ret
}

func showErrorBox(text string) {
	t, _ := syscall.UTF16PtrFromString("NSM - Powered by AFX")
	m, _ := syscall.UTF16PtrFromString(text)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x00000010)
}

func specialFolderPath(csidl int) string {
	var buf [260]uint16
	r, _, _ := procSHGetSpecialFolderPathW.Call(0, uintptr(unsafe.Pointer(&buf[0])), uintptr(csidl), 0)
	if r == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:])
}

func createShortcut(lnkPath, targetPath, workDir, desc string) {
	if lnkPath == "" || targetPath == "" {
		return
	}
	procCoInitializeEx.Call(0, 2) // COINIT_APARTMENTTHREADED
	defer procCoUninitialize.Call()

	var psl uintptr
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)),
		0,
		1, // CLSCTX_INPROC_SERVER
		uintptr(unsafe.Pointer(&iidIShellLinkW)),
		uintptr(unsafe.Pointer(&psl)),
	)
	if int32(hr) < 0 || psl == 0 {
		return
	}
	vtable := (*[24]uintptr)(unsafe.Pointer(*(*uintptr)(unsafe.Pointer(psl))))
	defer syscall.SyscallN(vtable[2], psl) // Release

	targetU16, _ := syscall.UTF16PtrFromString(targetPath)
	workU16, _ := syscall.UTF16PtrFromString(workDir)
	descU16, _ := syscall.UTF16PtrFromString(desc)

	syscall.SyscallN(vtable[20], psl, uintptr(unsafe.Pointer(targetU16)))   // SetPath
	syscall.SyscallN(vtable[9], psl, uintptr(unsafe.Pointer(workU16)))      // SetWorkingDirectory
	syscall.SyscallN(vtable[7], psl, uintptr(unsafe.Pointer(descU16)))      // SetDescription
	syscall.SyscallN(vtable[17], psl, uintptr(unsafe.Pointer(targetU16)), 0) // SetIconLocation

	var ppf uintptr
	hr, _, _ = syscall.SyscallN(vtable[0], psl, uintptr(unsafe.Pointer(&iidIPersistFile)), uintptr(unsafe.Pointer(&ppf)))
	if int32(hr) < 0 || ppf == 0 {
		return
	}
	pfVtable := (*[10]uintptr)(unsafe.Pointer(*(*uintptr)(unsafe.Pointer(ppf))))
	defer syscall.SyscallN(pfVtable[2], ppf) // Release

	lnkU16, _ := syscall.UTF16PtrFromString(lnkPath)
	syscall.SyscallN(pfVtable[6], ppf, uintptr(unsafe.Pointer(lnkU16)), 1) // Save
}

func ensureShortcuts(appExe, appDir string) {
	const (
		CSIDL_PROGRAMS         = 0x0002
		CSIDL_DESKTOPDIRECTORY = 0x0010
	)
	if desktop := specialFolderPath(CSIDL_DESKTOPDIRECTORY); desktop != "" {
		createShortcut(filepath.Join(desktop, "NSM.lnk"), appExe, appDir, "NSM - Powered by AFX")
	}
	if programs := specialFolderPath(CSIDL_PROGRAMS); programs != "" {
		createShortcut(filepath.Join(programs, "NSM.lnk"), appExe, appDir, "NSM - Powered by AFX")
	}
}

func readSFXTrailer(f *os.File) (payloadOffset uint64, payloadSize uint64, buildID string, ok bool) {
	st, err := f.Stat()
	if err != nil || st.Size() < sfxTrailerSize {
		return 0, 0, "", false
	}
	var buf [sfxTrailerSize]byte
	if _, err := f.ReadAt(buf[:], st.Size()-sfxTrailerSize); err != nil {
		return 0, 0, "", false
	}
	if string(buf[40:48]) != sfxMagic {
		return 0, 0, "", false
	}
	payloadOffset = binary.LittleEndian.Uint64(buf[0:8])
	payloadSize = binary.LittleEndian.Uint64(buf[8:16])
	buildID = strings.TrimRight(string(buf[16:40]), "\x00 ")
	return payloadOffset, payloadSize, buildID, true
}

func cleanObsoleteFiles(appDir string) {
	for _, rel := range []string{
		"NSM-Masterlist.exe",
		"repair-info",
		"local-first-logs",
		"README-LOCALTEST.txt",
		"DIAGNOSTIC-NOTES.txt",
		"START-LOCALHOST.bat",
		"START-LOCALHOST.ps1",
		"START-LOCAL-FIRST.bat",
		"START-LOCAL-FIRST.ps1",
		"VERSION.json",
		"RELEASE-NOTES.md",
		"SHA256SUMS.txt",
		"citizen",
		filepath.Join("data", "cache", "subprocess"),
	} {
		_ = os.RemoveAll(filepath.Join(appDir, rel))
	}
	entries, _ := os.ReadDir(appDir)
	for _, e := range entries {
		low := strings.ToLower(e.Name())
		if strings.HasPrefix(low, "citizen-") && strings.HasSuffix(low, ".dll") {
			_ = os.Remove(filepath.Join(appDir, e.Name()))
		} else if low == "citizengame.dll" || low == "citilaunch_tlsdummy.dll" || low == "cfx_curl_x86_64.dll" || low == "citizenfx.ini" {
			_ = os.Remove(filepath.Join(appDir, e.Name()))
		}
	}
}

func extractPayload(exeFile *os.File, exePath string, payloadOffset, payloadSize uint64, buildID, nsmRoot, appDir string) error {
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		return err
	}
	cleanObsoleteFiles(appDir)

	sec := io.NewSectionReader(exeFile, int64(payloadOffset), int64(payloadSize))
	zr, err := zip.NewReader(sec, int64(payloadSize))
	if err != nil {
		return fmt.Errorf("failed to read embedded NSM package: %w", err)
	}

	total := len(zr.File)
	if total == 0 {
		return fmt.Errorf("embedded NSM package is empty")
	}

	futureTime := time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)
	buf := make([]byte, 256*1024)

	for i, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		rel := strings.TrimPrefix(zf.Name, "NSM-Client/")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			continue
		}
		dest := filepath.Join(appDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}

		pct := ((i + 1) * 96) / total
		if i%3 == 0 || i == total-1 {
			setUIState("Installing NSM — Powered by AFX", "Extracting "+rel+"...", pct)
		}

		rc, err := zf.Open()
		if err != nil {
			return err
		}
		_ = os.Remove(dest)
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			rc.Close()
			return fmt.Errorf("could not write %s (is NSM already running?): %w", rel, err)
		}
		_, copyErr := io.CopyBuffer(out, rc, buf)
		closeErr := out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		_ = os.Chtimes(dest, futureTime, futureTime)
	}

	setUIState("Installing NSM — Powered by AFX", "Finalizing AppData installation & shortcuts...", 98)

	// Write lightweight launcher stub into %LOCALAPPDATA%\NSM\NSM.exe
	stubDest := filepath.Join(nsmRoot, "NSM.exe")
	if ! strings.EqualFold( filepath.Clean(exePath), filepath.Clean(stubDest) ) && payloadOffset > 0 {
		if stubBytes, err := io.ReadAll(io.NewSectionReader(exeFile, 0, int64(payloadOffset))); err == nil {
			var trailer [sfxTrailerSize]byte
			binary.LittleEndian.PutUint64(trailer[0:8], payloadOffset)
			binary.LittleEndian.PutUint64(trailer[8:16], 0)
			copy(trailer[16:40], []byte(buildID))
			copy(trailer[40:48], []byte(sfxMagic))
			_ = os.Remove(stubDest)
			_ = os.WriteFile(stubDest, append(stubBytes, trailer[:]...), 0o755)
		}
	}

	_ = os.WriteFile(filepath.Join(appDir, "NSM.installroot"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(appDir, ".nsm-version"), []byte(buildID+"\n"), 0o644)

	appExe := filepath.Join(appDir, "NSM.exe")
	ensureShortcuts(appExe, appDir)
	setUIState("Launching NSM...", "Ready!", 100)
	return nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func launchInstalledApp(appExe, appDir string) error {
	cmd := exec.Command(appExe, os.Args[1:]...)
	cmd.Dir = appDir
	return cmd.Start()
}

func main() {
	runtime.LockOSThread()

	exePath, err := os.Executable()
	if err != nil {
		showErrorBox("Failed to determine executable path: " + err.Error())
		return
	}
	exePath, _ = filepath.EvalSymlinks(exePath)

	localAppData := os.Getenv("LOCALAPPDATA")
	if localAppData == "" {
		if up := os.Getenv("USERPROFILE"); up != "" {
			localAppData = filepath.Join(up, "AppData", "Local")
		}
	}
	if localAppData == "" {
		showErrorBox("LOCALAPPDATA environment variable is not set.")
		return
	}

	nsmRoot := filepath.Join(localAppData, "NSM")
	appDir := filepath.Join(nsmRoot, "NSM.app")
	appExe := filepath.Join(appDir, "NSM.exe")
	coreRT := filepath.Join(appDir, "CoreRT.dll")
	rosCrt := filepath.Join(appDir, "AxiomFX", "ros", "ros.crt")
	verFile := filepath.Join(appDir, ".nsm-version")

	exeFile, err := os.Open(exePath)
	if err != nil {
		showErrorBox("Failed to open executable: " + err.Error())
		return
	}
	defer exeFile.Close()

	payloadOffset, payloadSize, buildID, hasTrailer := readSFXTrailer(exeFile)
	installedVerBytes, _ := os.ReadFile(verFile)
	installedVer := strings.TrimSpace(string(bytes.TrimSpace(installedVerBytes)))

	alreadyInstalled := fileExists(appExe) && fileExists(coreRT) && fileExists(rosCrt)
	upToDate := alreadyInstalled && (!hasTrailer || payloadSize == 0 || (buildID != "" && installedVer == buildID))

	if upToDate {
		ensureShortcuts(appExe, appDir)
		if err := launchInstalledApp(appExe, appDir); err != nil {
			showErrorBox("Failed to launch NSM from AppData:\n" + err.Error())
		}
		return
	}

	if !hasTrailer || payloadSize == 0 {
		if alreadyInstalled {
			if err := launchInstalledApp(appExe, appDir); err != nil {
				showErrorBox("Failed to launch NSM:\n" + err.Error())
			}
			return
		}
		showErrorBox("NSM installation files were not found in " + appDir + ". Please run the full NSM.exe installer.")
		return
	}

	// Register and show native Win32 progress window
	hInst, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("NSMInstallerWindowClass")
	wndTitle, _ := syscall.UTF16PtrFromString("NSM - Powered by AFX")
	hIcon, _, _ := procLoadIconW.Call(hInst, 1)
	hCursor, _, _ := procLoadCursorW.Call(0, 32512) // IDC_ARROW

	wcx := wndClassExW{
		Size:      uint32(unsafe.Sizeof(wndClassExW{})),
		Style:     0x0003,
		WndProc:   syscall.NewCallback(wndProc),
		Instance:  hInst,
		Icon:      hIcon,
		Cursor:    hCursor,
		ClassName: className,
		IconSm:    hIcon,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wcx)))

	screenW, _, _ := procGetSystemMetrics.Call(0)
	screenH, _, _ := procGetSystemMetrics.Call(1)
	winW, winH := uintptr(480), uintptr(185)
	posX := (screenW - winW) / 2
	posY := (screenH - winH) / 2

	const (
		WS_POPUP     = 0x80000000
		WS_CAPTION   = 0x00C00000
		WS_SYSMENU   = 0x00080000
		WS_MINIMIZE  = 0x00020000
		WS_VISIBLE   = 0x10000000
	)
	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(wndTitle)),
		WS_POPUP|WS_CAPTION|WS_SYSMENU|WS_MINIMIZE|WS_VISIBLE,
		posX, posY, winW, winH,
		0, 0, hInst, 0,
	)
	uiMu.Lock()
	uiHwnd = hwnd
	uiMu.Unlock()
	procShowWindow.Call(hwnd, 5)
	procUpdateWindow.Call(hwnd)

	var installErr error
	go func() {
		installErr = extractPayload(exeFile, exePath, payloadOffset, payloadSize, buildID, nsmRoot, appDir)
		if hwnd != 0 {
			procPostMessageW.Call(hwnd, 0x0010, 0, 0) // WM_CLOSE
		}
	}()

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	if installErr != nil {
		showErrorBox("Failed to install NSM to AppData:\n" + installErr.Error())
		return
	}

	if err := launchInstalledApp(appExe, appDir); err != nil {
		showErrorBox("Failed to launch NSM:\n" + err.Error())
	}
}
