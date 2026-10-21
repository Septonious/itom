# Frozen DEV2 checkpoint

source/ is exactly the source tree ff45334bbfbdafc7fef43df2e31000a21f11e684 from build commit37d3fae1b56213a8c6541e85ad64fa294155142e. Its APK/native revision is itom-1-1-0-dev2. It remains an unqualified development checkpoint; see ../../RESULTS-DEV2.md and ../../DEV3_BUILD.json for distinct evidence.

Current experiments explicitly use this frozen input, then compare immutable original RC2 production with the generated model. They never use the mutable current source/ as a baseline. qualification-workflow.yml preserves the earlier DEV2 workflow with its source path adjusted to this checkpoint. The active testing-build workflow is .github/workflows/itom-v110-dev3.yml.
