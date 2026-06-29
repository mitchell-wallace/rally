# Relay Runner Baseline — `decompose-relay-runner` tasks 1.1–1.4

Captured before the package split so later laps can diff against these
numbers and verify no test, exported symbol, or coverage is lost.

## 1.1 Test status

```
go test -count=1 ./internal/relay ./cmd/rally
```

**Result: GREEN** (both packages pass).

## 1.2 Exported-identifier checksum

Current `internal/relay` exports (from `go doc -all`).
After the split, `CancellationSource` (and its consts) plus `Config`,
`Runner`, `NewRunner`, `Runner.Run`, `Runner.SetTelemetry`,
`Runner.RequestStop` move to `internal/relay/runner`; everything else
stays in `internal/relay`. No new exported route-label constants/helpers
should appear.

### Constants

| Identifier | Source file |
| --- | --- |
| FreezeDuration | constants.go |
| PauseDuration | constants.go |
| HourlyRetriesBeforeFreeze | constants.go |
| HourlyRetryMaxAttempts | constants.go |
| BenchDefaultDuration | constants.go |
| StateActive | resilience.go |
| StatePaused | resilience.go |
| StateFrozen | resilience.go |
| StateProbation | resilience.go |
| StateBenched | resilience.go |
| CancellationSourceNone | runner.go → runner |
| CancellationSourceSkip | runner.go → runner |
| CancellationSourceGracefulStop | runner.go → runner |
| CancellationSourceQuitNow | runner.go → runner |

### Types

| Type | Source file |
| --- | --- |
| AgentMix | mix.go |
| Resolver | mix.go |
| AgentState | resilience.go |
| ResilienceKey | resilience.go |
| Resilience | resilience.go |
| CancellationSource | runner.go → runner |
| Config | runner.go → runner |
| Runner | runner.go → runner |

### Functions & Methods

| Signature | Source file |
| --- | --- |
| ParseAgentMix(specs, resolver) | mix.go |
| CreateRelay(s, targetIterations, agentMix) | relay.go |
| ResumeRelay(s) | relay.go |
| CompleteRelay(s, relayID) | relay.go |
| FormatMixLabel(stored) | route_runtime.go → mix.go |
| KeyFromAgent(a) | resilience.go |
| (ResilienceKey).String() | resilience.go |
| NewResilience(s) | resilience.go |
| (Resilience).GetState(key) | resilience.go |
| (Resilience).SelectActiveAgent(mix, runIndex) | resilience.go |
| (Resilience).PauseAgent(key, relayID) | resilience.go |
| (Resilience).UnpauseAgent(key, relayID) | resilience.go |
| (Resilience).RecordHourlyFailure(key, relayID) | resilience.go |
| (Resilience).FreezeAgent(key, relayID, reason) | resilience.go |
| (Resilience).BenchAgent(key, resetAt, scope, relayID) | resilience.go |
| NewRunner(s, cfg, executors) | runner.go → runner |
| (Runner).Run(ctx) | runner.go → runner |
| (Runner).SetTelemetry(sink) | runner.go → runner |
| (Runner).RequestStop() | runner.go → runner |
| (CancellationSource).String() | runner.go → runner |

## 1.3 Test / Benchmark inventory

**Total: 329** functions across 19 test files.

### Per-file summary

| File | Count |
| --- | --- |
| runner_test.go | 143 |
| route_runtime_test.go | 36 |
| runner_failure_telemetry_test.go | 30 |
| resilience_test.go | 30 |
| runner_timeout_runone_test.go | 13 |
| runner_outcome_test.go | 10 |
| runner_real_backend_test.go | 8 |
| runner_telemetry_test.go | 7 |
| runner_tally_test.go | 7 |
| runner_action_loop_test.go | 7 |
| runner_final_snippet_test.go | 6 |
| route_runtime_provider_test.go | 6 |
| log_test.go | 6 |
| runner_role_slot_test.go | 5 |
| bench_state_machine_test.go | 5 |
| runner_timeout_test.go | 4 |
| runner_hourly_retry_test.go | 4 |
| runner_real_laps_test.go | 1 |
| leftover_summary_test.go | 1 |

### Full inventory (file → function)

| File | Function |
| --- | --- |
| internal/relay/bench_state_machine_test.go | TestBenchMatrix_A_BenchedKeyNotSelectedBeforeReset |
| internal/relay/bench_state_machine_test.go | TestBenchMatrix_B_AllBenchedWaitsNotAllFrozen |
| internal/relay/bench_state_machine_test.go | TestBenchMatrix_C_PersistedBenchSurvivesFreshRelay |
| internal/relay/bench_state_machine_test.go | TestBenchMatrix_D_RepeatUsageLimitReBenchesFreshWindow |
| internal/relay/bench_state_machine_test.go | TestBenchMatrix_E_BenchedDoesNotInterfereWithOtherStates |
| internal/relay/leftover_summary_test.go | TestRelay_LeftoverSummaryFailoverCommitsAndEmitsDiagnostic |
| internal/relay/log_test.go | TestRepoDisplayName_FallbackDoesNotAffectRepoKey |
| internal/relay/log_test.go | TestRepoKey_Deterministic |
| internal/relay/log_test.go | TestRepoKey_DistinctPathsDistinctKeys |
| internal/relay/log_test.go | TestRepoKey_Format |
| internal/relay/log_test.go | TestRepoKey_LogPathScoping |
| internal/relay/log_test.go | TestRepoNameFromRemote |
| internal/relay/resilience_test.go | TestResilience_BenchAgent_WritesBenchedEvent |
| internal/relay/resilience_test.go | TestResilience_FreezeAgent_NoOpWhenAlreadyFrozen |
| internal/relay/resilience_test.go | TestResilience_FreezeAgent_WritesFrozenEvent |
| internal/relay/resilience_test.go | TestResilience_GetState_ActiveByDefault |
| internal/relay/resilience_test.go | TestResilience_GetState_BenchedBeforeResetDeadline |
| internal/relay/resilience_test.go | TestResilience_GetState_BenchedDecaysToActiveAfterReset |
| internal/relay/resilience_test.go | TestResilience_GetState_FrozenAfterFreezeEvent |
| internal/relay/resilience_test.go | TestResilience_GetState_FrozenDecaysToProbation |
| internal/relay/resilience_test.go | TestResilience_GetState_FrozenNotDecayed |
| internal/relay/resilience_test.go | TestResilience_GetState_PausedAfterPauseEvent |
| internal/relay/resilience_test.go | TestResilience_PauseAgent_NoOpWhenAlreadyPaused |
| internal/relay/resilience_test.go | TestResilience_PauseAgent_WritesPausedEvent |
| internal/relay/resilience_test.go | TestResilience_ProbationFailure_ReFreezesWithFreshTimestamp |
| internal/relay/resilience_test.go | TestResilience_ProbationIncomplete_PromotesToActive |
| internal/relay/resilience_test.go | TestResilience_ProbationSuccess_PromotesToActive |
| internal/relay/resilience_test.go | TestResilience_RecordHourlyFailure_CountBreaksAtActiveBoundary |
| internal/relay/resilience_test.go | TestResilience_RecordHourlyFailure_CountBreaksAtFrozenBoundary |
| internal/relay/resilience_test.go | TestResilience_RecordHourlyFailure_CountsAndAutoFreezes |
| internal/relay/resilience_test.go | TestResilience_ResilienceKey_String |
| internal/relay/resilience_test.go | TestResilience_SelectActiveAgent_AllFrozenButDecayable |
| internal/relay/resilience_test.go | TestResilience_SelectActiveAgent_AllFrozenError |
| internal/relay/resilience_test.go | TestResilience_SelectActiveAgent_CyclesThroughActive |
| internal/relay/resilience_test.go | TestResilience_SelectActiveAgent_EmptyCycle |
| internal/relay/resilience_test.go | TestResilience_SelectActiveAgent_PausedButExpired_ReturnsAsRetry |
| internal/relay/resilience_test.go | TestResilience_SelectActiveAgent_PausedNotExpired_SkipsAgent |
| internal/relay/resilience_test.go | TestResilience_SelectActiveAgent_SkipsPausedAndFrozen |
| internal/relay/resilience_test.go | TestResilience_StateTransition_FrozenStaysFrozen |
| internal/relay/resilience_test.go | TestResilience_StateTransition_PausedToFrozen |
| internal/relay/resilience_test.go | TestResilience_UnpauseAgent_NoOpWhenActive |
| internal/relay/resilience_test.go | TestResilience_UnpauseAgent_RestoresActive |
| internal/relay/route_runtime_provider_test.go | TestApplyProviders_WarnsOnDisabledProvider |
| internal/relay/route_runtime_provider_test.go | TestBenchQuotaScope_ProviderGroupSpansHarnesses |
| internal/relay/route_runtime_provider_test.go | TestProviderDisabled_AllDisabledLaneTerminates |
| internal/relay/route_runtime_provider_test.go | TestProviderDisabled_NotUnbenchedBySync |
| internal/relay/route_runtime_provider_test.go | TestProviderDisabled_SidelinesEntriesAcrossLanes |
| internal/relay/route_runtime_provider_test.go | TestProviderDisabled_StaysDisabledAfterForceUnpause |
| internal/relay/route_runtime_test.go | TestBenchQuotaScope_BenchesEveryKeyInScopeAcrossLanes |
| internal/relay/route_runtime_test.go | TestBenchQuotaScope_OpencodeProviderScopes |
| internal/relay/route_runtime_test.go | TestBenchResetDeadline |
| internal/relay/route_runtime_test.go | TestForceUnpauseAll_ClearsBenchOnSkip |
| internal/relay/route_runtime_test.go | TestFormatMixLabel |
| internal/relay/route_runtime_test.go | TestHasProbationEventForCurrentFreeze |
| internal/relay/route_runtime_test.go | TestRouteRuntime_ActiveExhaustedEntryStaysAdvanced |
| internal/relay/route_runtime_test.go | TestRouteRuntime_CanonicalScenario1_NoQuotasRunUntilFailure |
| internal/relay/route_runtime_test.go | TestRouteRuntime_CanonicalScenario2_MixedQuotaThenNoQuotaFallback |
| internal/relay/route_runtime_test.go | TestRouteRuntime_CanonicalScenario3_AssigneeFallsBackToDefault |
| internal/relay/route_runtime_test.go | TestRouteRuntime_CanonicalScenario4_MissingDefaultErrorsForUnmatchedRole |
| internal/relay/route_runtime_test.go | TestRouteRuntime_CanonicalScenario5_OverrideIgnoresAssigneeForEntireRelay |
| internal/relay/route_runtime_test.go | TestRouteRuntime_CanonicalScenario6_OverrideRoleReferenceAdvancesDefaultCursor |
| internal/relay/route_runtime_test.go | TestRouteRuntime_CanonicalScenario7_RangeQuotaWaitsWhenAllOthersPaused |
| internal/relay/route_runtime_test.go | TestRouteRuntime_ForceUnpauseAll |
| internal/relay/route_runtime_test.go | TestRouteRuntime_ForceUnpauseAllMixedStates |
| internal/relay/route_runtime_test.go | TestRouteRuntime_MissingRecoveryRouteWarnsAndFallsBack |
| internal/relay/route_runtime_test.go | TestRouteRuntime_MixedLanesWarnsOnlySingleRunner |
| internal/relay/route_runtime_test.go | TestRouteRuntime_MultiRunnerLaneDoesNotWarn |
| internal/relay/route_runtime_test.go | TestRouteRuntime_NoBackendAlwaysUsesDefaultRoute |
| internal/relay/route_runtime_test.go | TestRouteRuntime_OrdinaryFailedDoesNotForceRecovery |
| internal/relay/route_runtime_test.go | TestRouteRuntime_OverridePrecedenceOverRecoveryRoute |
| internal/relay/route_runtime_test.go | TestRouteRuntime_PausedExpiryResetsExhaustedEntry |
| internal/relay/route_runtime_test.go | TestRouteRuntime_ProbationOneShotEnforcement |
| internal/relay/route_runtime_test.go | TestRouteRuntime_ProbationOneShotSyncRecoverySignals |
| internal/relay/route_runtime_test.go | TestRouteRuntime_ReasoningResolvedPauseWaitAndForceUnpauseUseVariantKey |
| internal/relay/route_runtime_test.go | TestRouteRuntime_ReasoningResolvedProbationUsesVariantKey |
| internal/relay/route_runtime_test.go | TestRouteRuntime_ReasoningResolvedQuotaBenchUsesVariantKey |
| internal/relay/route_runtime_test.go | TestRouteRuntime_RecoveryCapFallsBackAndRaisesFlag |
| internal/relay/route_runtime_test.go | TestRouteRuntime_RecoveryPendingFollowupUsesOriginalTrigger |
| internal/relay/route_runtime_test.go | TestRouteRuntime_RecoveryPendingRoutesToRecovery |
| internal/relay/route_runtime_test.go | TestRouteRuntime_RecoveryStateSurvivesStoreReload |
| internal/relay/route_runtime_test.go | TestRouteRuntime_SingleRunnerLaneWarns |
| internal/relay/route_runtime_test.go | TestRouteRuntime_SingleRunnerOverrideWarns |
| internal/relay/route_runtime_test.go | TestSelectionWaitError_AllBenchedLaneWaitsNotFrozen |
| internal/relay/route_runtime_test.go | TestSyncRecoverySignals_BenchedEntryKeptOutOfRotation |
| internal/relay/runner_action_loop_test.go | TestActionLoopArmsFirstPressThenConfirms |
| internal/relay/runner_action_loop_test.go | TestActionLoopPauseCapturesSessionID |
| internal/relay/runner_action_loop_test.go | TestActionLoopQuitCancelsAndAbortsWithoutWaiting |
| internal/relay/runner_action_loop_test.go | TestActionLoopSecondQuitForceKills |
| internal/relay/runner_action_loop_test.go | TestActionLoopSkipReturnsResultAndSetsFlag |
| internal/relay/runner_action_loop_test.go | TestActionLoopStalledAttemptQuitsPromptly |
| internal/relay/runner_action_loop_test.go | TestActionLoopStopCancelsAndDrains |
| internal/relay/runner_failure_telemetry_test.go | TestLastOutputAgeUsesTryLogMtimeWhenAvailable |
| internal/relay/runner_failure_telemetry_test.go | TestRun_AllFrozen_CapturesFrozenState |
| internal/relay/runner_failure_telemetry_test.go | TestRun_AllFrozen_CarriesRallyContext |
| internal/relay/runner_failure_telemetry_test.go | TestRunBoundedHandoffOnly_AppendTryFailureDoesNotEmitRallyTry |
| internal/relay/runner_failure_telemetry_test.go | TestRunBoundedHandoffOnly_PersistedTryEmitsExactlyOneRallyTry |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_AgentClassFailureStaysSpanLogOnly |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_AppendTryFailureDoesNotEmitRallyTry |
| internal/relay/runner_failure_telemetry_test.go | TestRunOneBudgetKillWithDirtyTreeEmitsOperatorWorthyCapture |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_CancelledLapsAttemptDoesNotCaptureIncompleteFinalization |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_ExecErrorWithLogPatternUsesTextPatternEvidence |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_ExecErrorWithoutEvidenceUsesClassifierEvidence |
| internal/relay/runner_failure_telemetry_test.go | TestRunOneHandoffTimeoutOutcomeStaysSpanLogOnly |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_LapPinMismatchTelemetryIsWarningDiagnostic |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_LimitSignalDiagnostic_EmittedWithoutIssue |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_OrdinaryAgentErrorEvidenceStaysTryTelemetryOnly |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_PersistedTryEmitsExactlyOneRallyTry |
| internal/relay/runner_failure_telemetry_test.go | TestRunOneRecoveryClassificationTelemetryAndNeedsUserIssue |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_ResolvedModelBareAliasPropagatesToFailureDiagnosticAndTryTags |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_TerminalTryFailure_EnrichesUsageLimitState |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_TerminalTryFailure_NonLimitCategory_BoundedEvidenceContext |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_TerminalTryFailure_ProviderOverloaded |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_TerminalTryFailure_ScrubsHomePathInRawSignal |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_TerminalTryFailure_ShortRateLimit |
| internal/relay/runner_failure_telemetry_test.go | TestRunOneTimeoutHandoffOutcomesStaySpanLogOnly |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_UnfinalizedAgent_CapturesIncompleteFinalization |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_UnfinalizedAgentDirtyTree_EmitsDirtyTreeEvidence |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_UnfinalizedAgent_MultiAttemptBudget |
| internal/relay/runner_failure_telemetry_test.go | TestRunOne_UnfinalizedCaptureUsesResolvedModelRunnerTag |
| internal/relay/runner_failure_telemetry_test.go | TestRunRecoveryCapHitCapturesNeedsUserIssue |
| internal/relay/runner_failure_telemetry_test.go | TestRun_RouteFallbackTelemetryIncludesTriggerCause |
| internal/relay/runner_final_snippet_test.go | TestNormalizeFinalSnippetIgnoresOlderRunSummaryEntry |
| internal/relay/runner_final_snippet_test.go | TestNormalizeFinalSnippetUsesExplicitIndicatorsWithoutUsableText |
| internal/relay/runner_final_snippet_test.go | TestRunOneFinalSnippetExecutorFallbackConsistentAcrossRetryAndRecords |
| internal/relay/runner_final_snippet_test.go | TestRunOneFinalSnippetUsesBoundedLogTailFallback |
| internal/relay/runner_final_snippet_test.go | TestRunOneFinalSnippetUsesRecordedWrapupSummary |
| internal/relay/runner_final_snippet_test.go | TestRunOneNonOpenCodeInvalidStructuredResultDoesNotPersistTranscript |
| internal/relay/runner_hourly_retry_test.go | TestHourlyRetryBudgetHonored |
| internal/relay/runner_hourly_retry_test.go | TestHourlyRetryTransientFailureDoesNotBurnFreezeLife |
| internal/relay/runner_hourly_retry_test.go | TestPerHarnessModelCascadeIsolation |
| internal/relay/runner_hourly_retry_test.go | TestResetAgentStatus_ClearsAllStates |
| internal/relay/runner_outcome_test.go | TestRunGracefulStopCancellationPersistsAndStopsRelay |
| internal/relay/runner_outcome_test.go | TestRunOneCleanHandoffDoesNotSetDirtyRecoveryMetadata |
| internal/relay/runner_outcome_test.go | TestRunOneIncompleteAndOrdinaryFailedDoNotSetDirtyHandoff |
| internal/relay/runner_outcome_test.go | TestRunOneLapPinMismatchIsWarningOnly |
| internal/relay/runner_outcome_test.go | TestRunOneOperatorCancellationPersistsCancelledNotFailed |
| internal/relay/runner_outcome_test.go | TestRunOneRecordsHandoffRequestedOutcome |
| internal/relay/runner_outcome_test.go | TestRunOneRecoveryClassificationPersistence |
| internal/relay/runner_outcome_test.go | TestRunOneRecoveryEffectiveAssigneeUsesRecoveryPromptAndKeepsLapAssignee |
| internal/relay/runner_outcome_test.go | TestRunOneRetryThenDirtyHandoffUsesRunScopedDirtyDetection |
| internal/relay/runner_outcome_test.go | TestTryOutcomeForAttemptLifecycleBoundaries |
| internal/relay/runner_real_backend_test.go | TestRealBackend_AntigravityRelay |
| internal/relay/runner_real_backend_test.go | TestRealBackend_ClaudeBasicRelay |
| internal/relay/runner_real_backend_test.go | TestRealBackend_ClaudeWithLaps |
| internal/relay/runner_real_backend_test.go | TestRealBackend_CodexRelay |
| internal/relay/runner_real_backend_test.go | TestRealBackend_CustomHarnessRelay |
| internal/relay/runner_real_backend_test.go | TestRealBackend_LogScopingPerRepo |
| internal/relay/runner_real_backend_test.go | TestRealBackend_OpenCodeRelay |
| internal/relay/runner_real_backend_test.go | TestRealBackend_ResilienceRetryBudget |
| internal/relay/runner_real_laps_test.go | TestRunnerUsesRealLapsHeadTask |
| internal/relay/runner_role_slot_test.go | TestResolveRecoveryRoleInstructionsOnDiskOverrides |
| internal/relay/runner_role_slot_test.go | TestResolveRoleInstructionsDisabledWithoutLaps |
| internal/relay/runner_role_slot_test.go | TestResolveRoleInstructionsEmbeddedDefault |
| internal/relay/runner_role_slot_test.go | TestResolveRoleInstructionsOnDiskOverrides |
| internal/relay/runner_role_slot_test.go | TestResolveRoleInstructionsUnknownRole |
| internal/relay/runner_tally_test.go | TestTallyRunsAggregatesMultipleRuns |
| internal/relay/runner_tally_test.go | TestTallyRunsAllCancellationSourcesNotFailed |
| internal/relay/runner_tally_test.go | TestTallyRunsAllExhausted |
| internal/relay/runner_tally_test.go | TestTallyRunsCancelledNotFailed |
| internal/relay/runner_tally_test.go | TestTallyRunsCompletionWinsOverEarlierCancellation |
| internal/relay/runner_tally_test.go | TestTallyRunsEmpty |
| internal/relay/runner_tally_test.go | TestTallyRunsRetryThenSuccess |
| internal/relay/runner_telemetry_test.go | TestApplyRallyContext_SpanReceivesIdentityAndContext |
| internal/relay/runner_telemetry_test.go | TestFirstNonEmpty |
| internal/relay/runner_telemetry_test.go | TestLapPinMismatchDiagnosticEventCarriesReasonWithoutFailureCategory |
| internal/relay/runner_telemetry_test.go | TestRallyFailure_DoesNotMutateBaseTags |
| internal/relay/runner_telemetry_test.go | TestRallyFailure_TagsAndContext |
| internal/relay/runner_telemetry_test.go | TestRunnerLabel_ResolvedModelFallback |
| internal/relay/runner_telemetry_test.go | TestRunner_rallyContext |
| internal/relay/runner_test.go | TestAgentCyclingDeterminism |
| internal/relay/runner_test.go | TestAgentMixMixedForms |
| internal/relay/runner_test.go | TestAgentMixMixedNamedAndWeighted |
| internal/relay/runner_test.go | TestAgentMixNamedModels |
| internal/relay/runner_test.go | TestAgentUnfreeze |
| internal/relay/runner_test.go | TestAllAgentsFrozenEndsRelay |
| internal/relay/runner_test.go | TestBuildLivenessProbeDisabledByConfig |
| internal/relay/runner_test.go | TestBuildLivenessProbeEnabledForSupportedAdapter |
| internal/relay/runner_test.go | TestBuildLivenessProbeSkipsUnsupportedAdapter |
| internal/relay/runner_test.go | TestBuildRecentContextCancelledUsesOutcome |
| internal/relay/runner_test.go | TestBuildRecentContext_OverallTruncation |
| internal/relay/runner_test.go | TestBuildRecentContext_PerSummaryTruncation |
| internal/relay/runner_test.go | TestCategorizedTryRecordCarriesCategoryAndDisplayReason |
| internal/relay/runner_test.go | TestCombinedRelayAndRunScopedMessages |
| internal/relay/runner_test.go | TestCommitHashTracking_AgentCommitted |
| internal/relay/runner_test.go | TestCommitHashTracking_AutoCommitted |
| internal/relay/runner_test.go | TestCommitHashTracking_NoChanges |
| internal/relay/runner_test.go | TestCommitHistoryTracking_MultipleAgentCommits |
| internal/relay/runner_test.go | TestDetectLapsMarkerInText |
| internal/relay/runner_test.go | TestE2E_BackwardsCompat_RootModelFields |
| internal/relay/runner_test.go | TestE2E_CheapRotationOpencodeGLMToKimi |
| internal/relay/runner_test.go | TestE2E_ClaudeRateLimitWaitAndResume |
| internal/relay/runner_test.go | TestE2E_DefaultsSection_NoDeprecation |
| internal/relay/runner_test.go | TestE2E_ErrorPatternRotateAdvancesRoute |
| internal/relay/runner_test.go | TestE2E_ErrorPatternStrategies |
| internal/relay/runner_test.go | TestE2E_FullConfig_NamedModelsAndFallback |
| internal/relay/runner_test.go | TestE2E_LivenessProbeClearsFreezeFlag |
| internal/relay/runner_test.go | TestE2E_LivenessProbeFailureConfirmsFreeze |
| internal/relay/runner_test.go | TestE2E_RunStateClearedAtRelayStart |
| internal/relay/runner_test.go | TestE2E_SimulatedFreezeGracefulKillResumeRecovery |
| internal/relay/runner_test.go | TestE2E_UserDefinedHarness_BareAliasNoModel |
| internal/relay/runner_test.go | TestE2E_UserDefinedHarness_ModelFlagEmpty |
| internal/relay/runner_test.go | TestE2E_UserDefinedHarness_ModelFlagSet |
| internal/relay/runner_test.go | TestE2E_UserDefinedHarness_ModelFlagUnset_InfoNote |
| internal/relay/runner_test.go | TestE2E_WindowsFreezeDisabledRetryBudgetExhaustion |
| internal/relay/runner_test.go | TestExplicitSkipStartsFresh |
| internal/relay/runner_test.go | TestFailedRunDoesNotCountIteration |
| internal/relay/runner_test.go | TestFailureCascadeAgentErrorDoesNotIncrement |
| internal/relay/runner_test.go | TestFailureCascadeMultipleInfraIncrements |
| internal/relay/runner_test.go | TestFailureCascadeSingleInfraDoesNotIncrement |
| internal/relay/runner_test.go | TestFallbackInstructionsIgnoredInLapsMode |
| internal/relay/runner_test.go | TestFallbackInstructionsIgnoredWhenCLIPromptProvided |
| internal/relay/runner_test.go | TestFallbackInstructionsMissingFileUsesBuiltInDefault |
| internal/relay/runner_test.go | TestFallbackInstructionsUnconfiguredUsesBuiltInDefault |
| internal/relay/runner_test.go | TestFallbackInstructionsUsedInNoBackendMode |
| internal/relay/runner_test.go | TestFilesChangedListExcludesAllTransientPaths |
| internal/relay/runner_test.go | TestFilesChangedListExcludesClaudeSettings |
| internal/relay/runner_test.go | TestFilesChangedListFallsBackToDirtyFiles |
| internal/relay/runner_test.go | TestFilesChangedListUsesCommitDiff |
| internal/relay/runner_test.go | TestFormatCategorizedDisplay |
| internal/relay/runner_test.go | TestFormatRemaining |
| internal/relay/runner_test.go | TestFreezeCascade |
| internal/relay/runner_test.go | TestFreshStartRetryClearsRunState |
| internal/relay/runner_test.go | TestFreshStartRetryMidHandoffClearsFlag |
| internal/relay/runner_test.go | TestGracefulStop |
| internal/relay/runner_test.go | TestHourlyRetryWithOtherAgentActive |
| internal/relay/runner_test.go | TestIncompleteDoesNotCountTowardFailureCascade |
| internal/relay/runner_test.go | TestIncompleteLeftoverAware_NoChangeNoFinalize |
| internal/relay/runner_test.go | TestIncompleteLeftoverAware_NoOpInheritingLeftovers |
| internal/relay/runner_test.go | TestIncompleteLeftoverAware_OwnUnfinalizedChanges |
| internal/relay/runner_test.go | TestIncompleteLeftoverAware_TouchingInheritedLeftover |
| internal/relay/runner_test.go | TestIncompleteRetryCarriesFinalizationGuidance |
| internal/relay/runner_test.go | TestIncompleteRetryPromptGuidance |
| internal/relay/runner_test.go | TestIncompleteRunLeavesChangesUncommitted |
| internal/relay/runner_test.go | TestInstructionsPassedToExecutor |
| internal/relay/runner_test.go | TestLapAttemptRecordedInTryRecord |
| internal/relay/runner_test.go | TestLapPinMismatchClearsFailureClass |
| internal/relay/runner_test.go | TestLapPinMismatchCompletesWhenPinnedLapAlreadyDone |
| internal/relay/runner_test.go | TestLapPinMultiLapWarningInRunOne |
| internal/relay/runner_test.go | TestLapPinNormalPassThroughInRunOne |
| internal/relay/runner_test.go | TestLapPinValidation_DuplicateSameLap |
| internal/relay/runner_test.go | TestLapPinValidation_EmptyPinnedID |
| internal/relay/runner_test.go | TestLapPinValidation_MultiLapConsumed |
| internal/relay/runner_test.go | TestLapPinValidation_NoRecordedLaps |
| internal/relay/runner_test.go | TestLapPinValidation_NormalPassThrough |
| internal/relay/runner_test.go | TestLapPinValidation_WrongLapConsumed |
| internal/relay/runner_test.go | TestLapPinWrongLapWarningInRunOne |
| internal/relay/runner_test.go | TestLapsHeadTaskPassedToExecutor |
| internal/relay/runner_test.go | TestLapsInstructionsFileFallsBackToDefault |
| internal/relay/runner_test.go | TestLapsInstructionsFileUsed |
| internal/relay/runner_test.go | TestLapsInstructionsNotUsedInNoBackendMode |
| internal/relay/runner_test.go | TestLapsInstructionsUnconfiguredUsesDefault |
| internal/relay/runner_test.go | TestLeftoverWorkGuidance_CleanTree |
| internal/relay/runner_test.go | TestLeftoverWorkGuidance_DirtyTree |
| internal/relay/runner_test.go | TestLeftoverWorkGuidance_OnlyRallyDirty |
| internal/relay/runner_test.go | TestMessageConsumptionPerRun |
| internal/relay/runner_test.go | TestParseAgentMixAllFormsCombined |
| internal/relay/runner_test.go | TestParseAgentMixBareAlias |
| internal/relay/runner_test.go | TestParseAgentMixThirdColonSegmentRejected |
| internal/relay/runner_test.go | TestParseAgentMixUnknownHarnessError |
| internal/relay/runner_test.go | TestParseAgentMixUnresolvedModelError |
| internal/relay/runner_test.go | TestPerHarnessModelPauseIsolation |
| internal/relay/runner_test.go | TestProbationIncompletePromotesToActive |
| internal/relay/runner_test.go | TestProgressLapsCompletedForRunReadsSummaryJSONL |
| internal/relay/runner_test.go | TestPromptBudget_CountHonored |
| internal/relay/runner_test.go | TestPromptBudget_OverallLimit |
| internal/relay/runner_test.go | TestPromptBudget_PerSummaryTruncation |
| internal/relay/runner_test.go | TestPromptBudget_ShortSummariesPassThrough |
| internal/relay/runner_test.go | TestRelayScopedMessageAddressed |
| internal/relay/runner_test.go | TestRelayScopedMessageIncludedInAllRuns |
| internal/relay/runner_test.go | TestRenderRunFooterInterimRedrawsInPlace |
| internal/relay/runner_test.go | TestRenderRunFooterTerminalCommits |
| internal/relay/runner_test.go | TestResumeFromStoredLabelWithMixedForms |
| internal/relay/runner_test.go | TestResumeFromStoredLabelWithNamedModels |
| internal/relay/runner_test.go | TestResumeRetryMidHandoffPreservesFlag |
| internal/relay/runner_test.go | TestResumeRetryPassesSessionID |
| internal/relay/runner_test.go | TestResumeRetryPreservesRunState |
| internal/relay/runner_test.go | TestResumeReusesSessionIDOnNextAttempt |
| internal/relay/runner_test.go | TestResumeRoundTripWithRealResolver |
| internal/relay/runner_test.go | TestRetryWithinRun |
| internal/relay/runner_test.go | TestRoleInstructionsLoadedForAssignee |
| internal/relay/runner_test.go | TestRoleInstructionsMissingFileIsSilent |
| internal/relay/runner_test.go | TestRoleInstructionsSkippedInNoBackendMode |
| internal/relay/runner_test.go | TestRunBenchesOpencodeUsageLimitQuotaScopeNotAgentError |
| internal/relay/runner_test.go | TestRunFooterCadenceExhausted |
| internal/relay/runner_test.go | TestRunFooterCadenceRecovery |
| internal/relay/runner_test.go | TestRunFooterSingleAttemptColoursImmediately |
| internal/relay/runner_test.go | TestRunHeaderDoesNotExceedTargetAfterFailedRun |
| internal/relay/runner_test.go | TestRunnerCrossHarnessAdvanceDoesNotRotate |
| internal/relay/runner_test.go | TestRunnerDoesNotCreateRepoRelayLogDir |
| internal/relay/runner_test.go | TestRunnerNoBackendUsesDefaultRouteAndFallbackPrompt |
| internal/relay/runner_test.go | TestRunnerRotateModelErrorFallsBackToExecution |
| internal/relay/runner_test.go | TestRunnerRouteIntegration_AssigneesQuotasFreezeAndRoleFiles |
| internal/relay/runner_test.go | TestRunnerSameHarnessAdvanceUsesRotateModel |
| internal/relay/runner_test.go | TestRunOneEvidenceBeatsIncompleteClassification |
| internal/relay/runner_test.go | TestRunOneFreezeRetryResumesAndRecovers |
| internal/relay/runner_test.go | TestRunOneHonorsExecutorEvidence |
| internal/relay/runner_test.go | TestRunOneLapPinIgnoresStaleSummaryEntriesForSameRunID |
| internal/relay/runner_test.go | TestRunOneTerminalCategorySingleAttempt |
| internal/relay/runner_test.go | TestRunWritesActiveTryMetadataBeforeExecutor |
| internal/relay/runner_test.go | TestStallRecovery_ImplementationRoleRecovers |
| internal/relay/runner_test.go | TestStallRecovery_ImplementationStalledWithCommits_Recovers |
| internal/relay/runner_test.go | TestStallRecovery_VerifyRoleExcluded |
| internal/relay/runner_test.go | TestStallRecovery_VerifyStalledWithCommits_StaysFailed |
| internal/relay/runner_test.go | TestStubEntryOnIncompleteRun |
| internal/relay/runner_test.go | TestWaitLoopArmedPressShowsHint |
| internal/relay/runner_test.go | TestWaitLoopElapses |
| internal/relay/runner_test.go | TestWaitLoopRendersHintAndCountdown |
| internal/relay/runner_test.go | TestWaitLoopRendersOnNewLineSafely |
| internal/relay/runner_test.go | TestWaitLoopSkipOnAction |
| internal/relay/runner_test.go | TestWaitLoopStopOnQuit |
| internal/relay/runner_test.go | TestWaitWithCountdownCancellable |
| internal/relay/runner_test.go | TestWaitWithCountdownElapses |
| internal/relay/runner_timeout_runone_test.go | TestRunOneBoundedHandoffOnlyFailedContinuationRecordsHandoffTimeout |
| internal/relay/runner_timeout_runone_test.go | TestRunOneBoundedHandoffOnlyPartialHandoffRecordsHandoffTimeout |
| internal/relay/runner_timeout_runone_test.go | TestRunOneBoundedHandoffOnlyTimeoutRecordsHandoffTimeout |
| internal/relay/runner_timeout_runone_test.go | TestRunOneRunBudgetExpiredBeforeCooldownStopsRetries |
| internal/relay/runner_timeout_runone_test.go | TestRunOneRunBudgetIsCumulativeAcrossRetries |
| internal/relay/runner_timeout_runone_test.go | TestRunOneRunBudgetResumableContinuationRecordsSeparateHandoffTry |
| internal/relay/runner_timeout_runone_test.go | TestRunOneRunBudgetResumeSupportedWithoutSessionRecordsSingleHandoffTimeout |
| internal/relay/runner_timeout_runone_test.go | TestRunOneRunBudgetStopsRetries |
| internal/relay/runner_timeout_runone_test.go | TestRunOneStallPrecedesHardTimeout |
| internal/relay/runner_timeout_runone_test.go | TestRunOneTryCapKillWithExecutorEvidenceKeepsEvidenceCategory |
| internal/relay/runner_timeout_runone_test.go | TestRunOneTryCapRetriesWithinBudget |
| internal/relay/runner_timeout_runone_test.go | TestRunOneTryTimeoutAtOrAboveRunBudgetIsSubsumed |
| internal/relay/runner_timeout_runone_test.go | TestRunOneUnderBudgetCompletesNormally |
| internal/relay/runner_timeout_test.go | TestActionLoopRunBudgetCancelsAndStopsRetries |
| internal/relay/runner_timeout_test.go | TestActionLoopStallPrecedesTimeout |
| internal/relay/runner_timeout_test.go | TestActionLoopTryCapCancelsButLeavesBudget |
| internal/relay/runner_timeout_test.go | TestActionLoopUnderBudgetCompletesNormally |

## 1.4 Coverage baseline

```
go test -coverprofile=relay-cover-before.out ./internal/relay/...
go tool cover -func=relay-cover-before.out | grep total
```

**Total statement coverage: 88.1%**

The raw coverage profile is committed alongside this file as
`relay-cover-before.out` for later comparison.
