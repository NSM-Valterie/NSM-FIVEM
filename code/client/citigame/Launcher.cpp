/*
 * This file is part of the Cfx project - https://cfx.re/
 *
 * See LICENSE in the root of the source tree for information
 * regarding licensing.
 */

#include "StdInc.h"
#include "LauncherInterface.h"
#include "Launcher.h"
#include "CrossLibraryInterfaces.h"
#include "ResumeComponent.h"

#include "Hooking.Aux.h"

#include <ComponentLoader.h>
#include <CrossBuildRuntime.h>

#include <HostSharedData.h>
#include <CfxState.h>

#include <UUIState.h>

extern void DisableNvCache();

IGameSpecToHooks* g_hooksDLL;

__declspec(dllexport) void SetHooksDll(IGameSpecToHooks* dll);

bool LauncherInterface::PreLoadGame(void* cefSandbox)
{
	// if we don't have adhesive, set our gamePid
	static HostSharedData<CfxState> initState("CfxInitState");

	if (initState->IsMasterProcess())
	{
		const auto& map = ComponentLoader::GetInstance()->GetKnownComponents();

		if (map.find("adhesive") == map.end())
		{
			initState->gamePid = initState->initialGamePid;
		}
	}
	
	bool continueRunning = true;

	// initialize component instances
    if (!getenv("CitizenFX_ToolMode"))
    {
		DisableToolHelpScope scope;

        ComponentLoader::GetInstance()->ForAllComponents([&] (fwRefContainer<ComponentData> componentData)
        {
            for (auto& instance : componentData->GetInstances())
            {
                instance->Initialize();
            }
        });
    }

	SetHooksDll(g_hooksDLL);

	// and start running the game
	return continueRunning;
}

bool LauncherInterface::PostLoadGame(HMODULE hModule, void(**entryPoint)())
{
	const DWORD postLoadStart = GetTickCount();
	trace("NSM startup: PostLoadGame enter pid=%lu module=%p\n", GetCurrentProcessId(), (void*)hModule);

	static HostSharedData<UpdaterUIState> uuiState("CfxUUIState");

	uuiState->SetText(1, "Analyzing game data");
	uuiState->SetProgress(100.0);
	uuiState->OpenWhenExpired();
	trace("NSM startup: UUI open requested title='%s' progress=%.1f finalized=%d waitForExpiration=%d\n",
		uuiState->subtitle, uuiState->progress, uuiState->finalized ? 1 : 0, uuiState->waitForExpiration ? 1 : 0);

	bool continueRunning = true;

	{
		DisableToolHelpScope scope;
		trace("NSM startup: DoGameLoad begin\n");
		ComponentLoader::GetInstance()->DoGameLoad(hModule);
		trace("NSM startup: DoGameLoad complete elapsedMs=%lu\n", GetTickCount() - postLoadStart);
	}

	trace("NSM startup: InitFunctionBase::RunAll begin\n");
	InitFunctionBase::RunAll();
	trace("NSM startup: InitFunctionBase::RunAll complete elapsedMs=%lu\n", GetTickCount() - postLoadStart);
	trace("NSM startup: PostLoadGame return elapsedMs=%lu\n", GetTickCount() - postLoadStart);

	return continueRunning;
}

bool LauncherInterface::PreResumeGame()
{
	const DWORD resumeStart = GetTickCount();
	trace("NSM startup: PreResumeGame enter pid=%lu\n", GetCurrentProcessId());
	RunLifeCycleCallback([] (LifeCycleComponent* component)
	{
		component->PreResumeGame();
	});
	trace("NSM startup: PreResumeGame component callbacks complete elapsedMs=%lu\n", GetTickCount() - resumeStart);

	static HostSharedData<UpdaterUIState> uuiState("CfxUUIState");
	uuiState->Close();
	trace("NSM startup: UUI close requested finalized=%d waitForExpiration=%d\n",
		uuiState->finalized ? 1 : 0, uuiState->waitForExpiration ? 1 : 0);

	return true;
}

bool LauncherInterface::PreInitializeGame()
{
	std::thread([]()
	{
		DisableNvCache();
	})
	.detach();

	// make the component loader initialize
	ComponentLoader::GetInstance()->Initialize();

	// run callbacks on the component loader
    if (!getenv("CitizenFX_ToolMode"))
    {
		DisableToolHelpScope thScope;

        RunLifeCycleCallback([] (LifeCycleComponent* component)
        {
            component->PreInitGame();
        });
    }

	return true;
}

static LauncherInterface g_launcherInterface;

extern "C" __declspec(dllexport) ILauncherInterface* GetLauncherInterface()
{
	return &g_launcherInterface;
}

extern "C" __declspec(dllexport) int GetGameVersion()
{
	return xbr::GetGameBuild();
}
