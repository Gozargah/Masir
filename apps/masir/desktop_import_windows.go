package main

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/Gozargah/Masir/libs/masir-core/service"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func stopOfflineService() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	s, err := manager.OpenService(service.WindowsName)
	if err != nil {
		return err
	}
	defer s.Close()
	// Capture the process while SCM reports a valid PID. SERVICE_STOPPED can be
	// reported before ServiceMain returns and the executable image is released.
	var process windows.SERVICE_STATUS_PROCESS
	var needed uint32
	if err := windows.QueryServiceStatusEx(s.Handle, windows.SC_STATUS_PROCESS_INFO, (*byte)(unsafe.Pointer(&process)), uint32(unsafe.Sizeof(process)), &needed); err != nil {
		return err
	}
	var handle windows.Handle
	switch process.CurrentState {
	case windows.SERVICE_STOPPED:
		return nil
	case windows.SERVICE_RUNNING, windows.SERVICE_PAUSED, windows.SERVICE_PAUSE_PENDING, windows.SERVICE_CONTINUE_PENDING:
		var err error
		handle, err = windows.OpenProcess(windows.SYNCHRONIZE, false, process.ProcessId)
		if err != nil {
			return fmt.Errorf("wait for service process: %w", err)
		}
		defer windows.CloseHandle(handle)
	default:
		return errors.New("service is changing state; retry the offline update")
	}
	stop(s)
	status, err := s.Query()
	if err != nil {
		return err
	}
	if status.State != svc.Stopped {
		return errors.New("service did not fully stop for offline update")
	}
	event, err := windows.WaitForSingleObject(handle, 30000)
	if err != nil {
		return err
	}
	if event != windows.WAIT_OBJECT_0 {
		return errors.New("service process did not exit for offline update")
	}
	return nil
}
func startOfflineService() error { return restartService() }
