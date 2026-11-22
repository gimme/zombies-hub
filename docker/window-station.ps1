# docker/window-station.ps1 - runs a program in a Windows window station of its own, not the one the user works in:
# for the game tests (docker/test-games.sh), whose players' Chrome, headless though it is, captures the machine's mouse
# when its page does (Chrome's headless mode has real windows on Windows, which no one sees, and locks the pointer with
# them: ClipCursor, which confines the user's cursor to where the window would be). The cursor is its window station's,
# the same one for every desktop in it, so a desktop of its own is not enough (one in the user's station confined the
# cursor still); a window station but the user's has no mouse to confine, and ClipCursor does nothing there. What the
# program and those it starts do with their windows and the pointer then leaves the user's alone, and the page has the
# pointer all the same; Chrome draws with the GPU there as anywhere. The station and its desktop (zombies-test) go with
# their last process.
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File window-station.ps1 <args file> <log file>
#
# <args file> has the program and its arguments, a line each (so that none is read as PowerShell's own, or quoted
# twice). The program's output and errors are added to <log file>, it has no input and no console window, and its
# environment is this one's. This waits for it, and exits with its exit code.
#
# The program and all it starts are in a job that Windows ends as this ends (JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE): to
# stop them, end this process, and no other. Not taskkill /T, which takes for a process's children whatever says its
# number is their parent's: a number Windows gives again once a process is gone, so that it can end processes that were
# never this one's.
$ErrorActionPreference = 'Stop'
if ($args.Count -ne 2) { [Console]::Error.WriteLine('usage: window-station.ps1 <args file> <log file>'); exit 2 }

Add-Type @'
using System;
using System.ComponentModel;
using System.Runtime.InteropServices;
using System.Text;

public static class Bo1Desktop {
    [StructLayout(LayoutKind.Sequential)]
    struct SECURITY_ATTRIBUTES { public int nLength; public IntPtr lpSecurityDescriptor; public bool bInheritHandle; }
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    struct STARTUPINFO {
        public int cb; public string lpReserved; public string lpDesktop; public string lpTitle;
        public int dwX, dwY, dwXSize, dwYSize, dwXCountChars, dwYCountChars, dwFillAttribute, dwFlags;
        public short wShowWindow, cbReserved2; public IntPtr lpReserved2, hStdInput, hStdOutput, hStdError;
    }
    [StructLayout(LayoutKind.Sequential)]
    struct PROCESS_INFORMATION { public IntPtr hProcess, hThread; public int dwProcessId, dwThreadId; }
    [StructLayout(LayoutKind.Sequential)]
    struct JOBOBJECT_BASIC_LIMIT_INFORMATION {
        public long PerProcessUserTimeLimit, PerJobUserTimeLimit; public uint LimitFlags;
        public UIntPtr MinimumWorkingSetSize, MaximumWorkingSetSize; public uint ActiveProcessLimit;
        public UIntPtr Affinity; public uint PriorityClass, SchedulingClass;
    }
    [StructLayout(LayoutKind.Sequential)]
    struct IO_COUNTERS { public ulong ReadOps, WriteOps, OtherOps, ReadBytes, WriteBytes, OtherBytes; }
    [StructLayout(LayoutKind.Sequential)]
    struct JOBOBJECT_EXTENDED_LIMIT_INFORMATION {
        public JOBOBJECT_BASIC_LIMIT_INFORMATION Basic; public IO_COUNTERS Io;
        public UIntPtr ProcessMemoryLimit, JobMemoryLimit, PeakProcessMemoryUsed, PeakJobMemoryUsed;
    }

    [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern IntPtr CreateWindowStation(string name, int flags, uint access, IntPtr security);
    [DllImport("user32.dll")]
    static extern IntPtr GetProcessWindowStation();
    [DllImport("user32.dll", SetLastError = true)]
    static extern bool SetProcessWindowStation(IntPtr station);
    [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern bool GetUserObjectInformation(IntPtr handle, int index, StringBuilder info, int length, out int needed);
    [DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern IntPtr CreateDesktop(string name, IntPtr device, IntPtr devmode, int flags, uint access, IntPtr security);
    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern IntPtr CreateFile(string name, uint access, uint share, ref SECURITY_ATTRIBUTES security, uint disposition,
        uint flags, IntPtr template);
    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern bool CreateProcess(string application, StringBuilder commandLine, IntPtr processSecurity,
        IntPtr threadSecurity, bool inheritHandles, uint flags, IntPtr environment, string directory,
        ref STARTUPINFO startup, out PROCESS_INFORMATION process);
    [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
    static extern IntPtr CreateJobObject(IntPtr security, string name);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool SetInformationJobObject(IntPtr job, int kind, ref JOBOBJECT_EXTENDED_LIMIT_INFORMATION info, int length);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool AssignProcessToJobObject(IntPtr job, IntPtr process);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern uint ResumeThread(IntPtr thread);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool TerminateProcess(IntPtr process, uint code);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern uint WaitForSingleObject(IntPtr handle, uint ms);
    [DllImport("kernel32.dll", SetLastError = true)]
    static extern bool GetExitCodeProcess(IntPtr process, out uint code);

    // an argument as CommandLineToArgvW reads it back
    static string Quote(string a) {
        if (a.Length > 0 && a.IndexOfAny(new[] { ' ', '\t', '"' }) < 0) return a;
        var s = new StringBuilder("\"");
        int slashes = 0;
        foreach (char c in a) {
            if (c == '\\') { slashes++; continue; }
            s.Append('\\', c == '"' ? 2 * slashes + 1 : slashes).Append(c);
            slashes = 0;
        }
        return s.Append('\\', 2 * slashes).Append('"').ToString();
    }

    public static int Run(string desktop, string[] command, string log) {
        const uint GENERIC_ALL = 0x10000000, GENERIC_READ = 0x80000000, FILE_APPEND_DATA = 4, SHARE_ALL = 7;
        const uint OPEN_EXISTING = 3, OPEN_ALWAYS = 4, CREATE_NO_WINDOW = 0x08000000, INFINITE = 0xFFFFFFFF;
        const int STARTF_USESTDHANDLES = 0x100;
        // a window station of this logon session (the name is Windows own: only an administrator gives one), and
        // the desktop in it; the same ones each time, which go with their last process
        IntPtr ours = GetProcessWindowStation();
        IntPtr station = CreateWindowStation(null, 0, GENERIC_ALL, IntPtr.Zero);
        if (station == IntPtr.Zero) throw new Win32Exception(Marshal.GetLastWin32Error(), "CreateWindowStation");
        var name = new StringBuilder(256);
        int needed;
        const int UOI_NAME = 2;
        if (!GetUserObjectInformation(station, UOI_NAME, name, name.Capacity * 2, out needed))
            throw new Win32Exception(Marshal.GetLastWin32Error(), "the window station has no name");
        if (!SetProcessWindowStation(station)) throw new Win32Exception(Marshal.GetLastWin32Error(), "SetProcessWindowStation");
        IntPtr made = CreateDesktop(desktop, IntPtr.Zero, IntPtr.Zero, 0, GENERIC_ALL, IntPtr.Zero);
        int error = Marshal.GetLastWin32Error();
        SetProcessWindowStation(ours);
        if (made == IntPtr.Zero) throw new Win32Exception(error, "CreateDesktop " + desktop);
        desktop = name + "\\" + desktop;
        var inherit = new SECURITY_ATTRIBUTES { bInheritHandle = true };
        inherit.nLength = Marshal.SizeOf(inherit);
        IntPtr output = CreateFile(log, FILE_APPEND_DATA, SHARE_ALL, ref inherit, OPEN_ALWAYS, 0, IntPtr.Zero);
        IntPtr input = CreateFile("NUL", GENERIC_READ, SHARE_ALL, ref inherit, OPEN_EXISTING, 0, IntPtr.Zero);
        if (output == new IntPtr(-1) || input == new IntPtr(-1))
            throw new Win32Exception(Marshal.GetLastWin32Error(), "opening " + log);
        var startup = new STARTUPINFO { lpDesktop = desktop, dwFlags = STARTF_USESTDHANDLES, hStdInput = input,
            hStdOutput = output, hStdError = output };
        startup.cb = Marshal.SizeOf(startup);
        var line = new StringBuilder(string.Join(" ", Array.ConvertAll(command, Quote)));
        // the job, which only this process has a handle of (none inherits it): it closes as this process ends, however
        // it ends, and Windows ends the processes in it then. The program starts suspended, so that it is in the job
        // before it starts any other.
        const uint CREATE_SUSPENDED = 4, JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE = 0x2000;
        const int JobObjectExtendedLimitInformation = 9;
        IntPtr job = CreateJobObject(IntPtr.Zero, null);
        var limits = new JOBOBJECT_EXTENDED_LIMIT_INFORMATION();
        limits.Basic.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
        if (job == IntPtr.Zero ||
            !SetInformationJobObject(job, JobObjectExtendedLimitInformation, ref limits, Marshal.SizeOf(limits)))
            throw new Win32Exception(Marshal.GetLastWin32Error(), "making the job");
        PROCESS_INFORMATION process;
        if (!CreateProcess(null, line, IntPtr.Zero, IntPtr.Zero, true, CREATE_NO_WINDOW | CREATE_SUSPENDED, IntPtr.Zero,
                null, ref startup, out process))
            throw new Win32Exception(Marshal.GetLastWin32Error(), "starting " + command[0]);
        if (!AssignProcessToJobObject(job, process.hProcess)) {
            error = Marshal.GetLastWin32Error();
            TerminateProcess(process.hProcess, 1);
            throw new Win32Exception(error, "putting " + command[0] + " in the job");
        }
        ResumeThread(process.hThread);
        WaitForSingleObject(process.hProcess, INFINITE);
        uint code;
        return GetExitCodeProcess(process.hProcess, out code) ? (int)code : 1;
    }
}
'@

$command = @(Get-Content -LiteralPath $args[0] -Encoding UTF8 | Where-Object { $_ -ne '' })
if ($command.Count -eq 0) { [Console]::Error.WriteLine("window-station: $($args[0]) names no program"); exit 2 }
try {
    exit [Bo1Desktop]::Run('zombies-test', $command, $args[1])
} catch {
    [Console]::Error.WriteLine("window-station: $($_.Exception.Message)")
    exit 1
}
