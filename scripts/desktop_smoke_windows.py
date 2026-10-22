"""Windows package smoke helpers. Requires a native interactive Windows runner."""

import ctypes
from ctypes import wintypes
import subprocess
import time


def native_smoke(command, base, environment):
    """Gate the app into a kill-on-close Job before any application code runs."""
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    ntdll = ctypes.WinDLL("ntdll")

    class Limits(ctypes.Structure):
        _fields_ = [
            ("PerProcessUserTimeLimit", ctypes.c_int64),
            ("PerJobUserTimeLimit", ctypes.c_int64),
            ("LimitFlags", wintypes.DWORD),
            ("MinimumWorkingSetSize", ctypes.c_size_t),
            ("MaximumWorkingSetSize", ctypes.c_size_t),
            ("ActiveProcessLimit", wintypes.DWORD),
            ("Affinity", ctypes.c_size_t),
            ("PriorityClass", wintypes.DWORD),
            ("SchedulingClass", wintypes.DWORD),
        ]

    class IO(ctypes.Structure):
        _fields_ = [
            (name, ctypes.c_uint64)
            for name in (
                "ReadOperationCount",
                "WriteOperationCount",
                "OtherOperationCount",
                "ReadTransferCount",
                "WriteTransferCount",
                "OtherTransferCount",
            )
        ]

    class ExtendedLimits(ctypes.Structure):
        _fields_ = [
            ("BasicLimitInformation", Limits),
            ("IoInfo", IO),
            ("ProcessMemoryLimit", ctypes.c_size_t),
            ("JobMemoryLimit", ctypes.c_size_t),
            ("PeakProcessMemoryUsed", ctypes.c_size_t),
            ("PeakJobMemoryUsed", ctypes.c_size_t),
        ]

    class Accounting(ctypes.Structure):
        _fields_ = [
            (name, ctypes.c_int64)
            for name in (
                "TotalUserTime",
                "TotalKernelTime",
                "ThisPeriodTotalUserTime",
                "ThisPeriodTotalKernelTime",
            )
        ] + [
            (name, wintypes.DWORD)
            for name in (
                "TotalPageFaultCount",
                "TotalProcesses",
                "ActiveProcesses",
                "TotalTerminatedProcesses",
            )
        ]

    kernel.CreateJobObjectW.argtypes = [ctypes.c_void_p, wintypes.LPCWSTR]
    kernel.CreateJobObjectW.restype = wintypes.HANDLE
    kernel.SetInformationJobObject.argtypes = [
        wintypes.HANDLE,
        ctypes.c_int,
        ctypes.c_void_p,
        wintypes.DWORD,
    ]
    kernel.AssignProcessToJobObject.argtypes = [wintypes.HANDLE, wintypes.HANDLE]
    kernel.TerminateJobObject.argtypes = [wintypes.HANDLE, wintypes.UINT]
    kernel.QueryInformationJobObject.argtypes = [
        wintypes.HANDLE,
        ctypes.c_int,
        ctypes.c_void_p,
        wintypes.DWORD,
        ctypes.c_void_p,
    ]
    kernel.CloseHandle.argtypes = [wintypes.HANDLE]
    ntdll.NtResumeProcess.argtypes = [wintypes.HANDLE]
    ntdll.NtResumeProcess.restype = ctypes.c_long
    job = kernel.CreateJobObjectW(None, None)
    if not job:
        raise ctypes.WinError(ctypes.get_last_error())
    process = None
    try:
        limits = ExtendedLimits()
        limits.BasicLimitInformation.LimitFlags = 0x2000  # KILL_ON_JOB_CLOSE
        if not kernel.SetInformationJobObject(job, 9, ctypes.byref(limits), ctypes.sizeof(limits)):
            raise ctypes.WinError(ctypes.get_last_error())
        process = subprocess.Popen(
            command,
            cwd=base,
            env=environment,
            text=True,
            encoding="utf-8",
            errors="replace",
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            creationflags=0x4,
        )
        if not kernel.AssignProcessToJobObject(job, int(process._handle)):
            raise ctypes.WinError(ctypes.get_last_error())
        if ntdll.NtResumeProcess(int(process._handle)) < 0:
            raise RuntimeError("cannot resume smoke process after Job Object admission")
        output, _ = process.communicate(timeout=65)
        return process.returncode, output
    finally:
        # Includes WebView2 children and nested Runtime Jobs. Never use a global
        # image-name kill that could stop an unrelated user application.
        try:
            if process is not None and process.poll() is None:
                process.kill()
            if not kernel.TerminateJobObject(job, 1):
                raise ctypes.WinError(ctypes.get_last_error())
            if process is not None:
                process.wait(timeout=5)
            deadline = time.monotonic() + 5
            while True:
                accounting = Accounting()
                if not kernel.QueryInformationJobObject(
                    job, 1, ctypes.byref(accounting), ctypes.sizeof(accounting), None
                ):
                    raise ctypes.WinError(ctypes.get_last_error())
                if not accounting.ActiveProcesses:
                    break
                if time.monotonic() >= deadline:
                    raise RuntimeError("Windows smoke descendants did not exit")
                time.sleep(0.01)
        finally:
            kernel.CloseHandle(job)
