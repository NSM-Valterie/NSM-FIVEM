#include <StdInc.h>

#include <CfxState.h>
#include <CfxSubProcess.h>

#include <HostSharedData.h>

#include <jitasm.h>

#include <CL2LaunchMode.h>
#include <CrossBuildRuntime.h>

void Component_RunPreInit()
{
	static HostSharedData<CfxState> hostData("CfxInitState");

	bool debugMode = false;

#if defined(GTA_FIVE) || defined(IS_RDR3) || defined(GTA_NY)
#ifdef _DEBUG
	debugMode = true;
#endif

	if (wcsstr(GetCommandLineW(), L"+run_lc"))
	{
		debugMode = false;
	}

	bool five = false;

#ifdef GTA_FIVE
	five = true;
#endif

	const bool isMaster = hostData->IsMasterProcess();
	trace("NSM launch diagnostic: PreInit pid=%lu master=%d debug=%d\n", GetCurrentProcessId(), isMaster ? 1 : 0, debugMode ? 1 : 0);

	if (isMaster && !debugMode)
	{
		const int gameBuild = xbr::GetGameBuild();
		trace("NSM launch diagnostic: preparing GameProcess build=%d\n", gameBuild);
		auto processName = MakeCfxSubProcess(L"GameProcess.exe", fmt::sprintf(L"game_%d_aslr", gameBuild));

		STARTUPINFOW si = { 0 };
		si.cb = sizeof(si);

		PROCESS_INFORMATION pi = { 0 };

		BOOL result = CreateProcessW(processName, GetCommandLineW(), nullptr, nullptr, FALSE, CREATE_SUSPENDED, nullptr, nullptr, &si, &pi);

		if (result)
		{
			trace("NSM launch diagnostic: CreateProcess succeeded childPid=%lu\n", pi.dwProcessId);

			// set the PID and create the game thread
			hostData->gamePid = pi.dwProcessId;
			DWORD resumeResult = ResumeThread(pi.hThread);
			DWORD resumeError = (resumeResult == static_cast<DWORD>(-1)) ? GetLastError() : ERROR_SUCCESS;
			trace("NSM launch diagnostic: ResumeThread result=%lu error=%lu\n", resumeResult, resumeError);

			// wait for the game process to exit
			DWORD waitResult = WaitForSingleObject(pi.hProcess, INFINITE);
			DWORD waitError = (waitResult == WAIT_FAILED) ? GetLastError() : ERROR_SUCCESS;
			DWORD childExitCode = 0;
			BOOL gotExitCode = GetExitCodeProcess(pi.hProcess, &childExitCode);
			DWORD exitCodeError = gotExitCode ? ERROR_SUCCESS : GetLastError();
			trace("NSM launch diagnostic: child finished wait=%lu waitError=%lu exitCodeRead=%d exitCode=%lu exitError=%lu\n",
				waitResult, waitError, gotExitCode ? 1 : 0, childExitCode, exitCodeError);

			CloseHandle(pi.hThread);
			CloseHandle(pi.hProcess);
			TerminateProcess(GetCurrentProcess(), 0);
		}
		else
		{
			DWORD createError = GetLastError();
			trace("NSM launch diagnostic: CreateProcess failed error=%lu\n", createError);
		}
	}
	else if (isMaster && debugMode)
	{
		hostData->gamePid = GetCurrentProcessId();
	}
#else
	if (hostData->IsMasterProcess())
	{
		hostData->gamePid = GetCurrentProcessId();
	}
#endif
}
