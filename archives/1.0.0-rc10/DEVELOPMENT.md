# ITOM 1.0.0 RC10 development

Exact RC2 transport baseline plus the independently selected fixes documented in [audit/SELECTION.md](audit/SELECTION.md). Original quota/journal/wallet/recovery behavior remains. Package ID `org.itom.client`, versionCode10009, runtime `itom-1-0-0-rc10`.

**Not qualified yet.** No release artifacts until all unchanged strict CI gates pass. Baseline source and intermediate results are committed to the RC10 development branch. Red/green evidence and deferred patch context are preserved in audit/.

First candidate05383c2, run39150978359: core/recovery/race PASS, 2/12 full-capacity rounds PASS. Other rounds failed download or tail75Mbps floor; retained SINGLE max1406.951ms failed as well. Early10/20Mbps probes timed out. All builds skipped. Exact chronological summary is in evidence/05383c2-qualification.json. RC2's field stability does not guarantee the stricter retained-state lab cases.

Next: isolated A/B on retained SINGLE and32 only: unchanged reference vs4MiB cleanup read vsRC8 advisory selection. Each runs the same unchanged tail/RTT/socket gates. Experimental patches are applied only inside their runner; none changes the candidate or authorizes packaging. No bulk flight, growth, polling, timeout, physical quota or journal policy changes are bundled.
