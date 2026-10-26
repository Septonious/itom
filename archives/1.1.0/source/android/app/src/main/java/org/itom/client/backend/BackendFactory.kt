package org.itom.client.backend

import android.content.Context
import android.content.Intent
import android.net.VpnService
import org.itom.client.SessionBackend
import org.itom.client.VaultStore
import org.itom.domain.*
import android.util.AtomicFile
import java.io.File
import org.json.JSONObject

fun createBackend(context: Context, vault: VaultStore): SessionBackend = NativeBackend(context, vault)

class NativeBackend(private val context: Context, val vault: VaultStore) : SessionBackend {
    val machine = SessionMachine()
    val adminState = kotlinx.coroutines.flow.MutableStateFlow("{}")
    override val admin: kotlinx.coroutines.flow.StateFlow<String> = adminState
    @Volatile var adminClient: org.itom.nativecore.mobile.Client? = null
    override suspend fun adminCall(request: String): String = kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) {
        val client = adminClient ?: throw DomainError("ADMIN_CONNECTION_FAILED")
        try { client.adminCall(request) } catch (e: kotlinx.coroutines.CancellationException) { throw e } catch (_: Exception) { throw DomainError("ADMIN_CONNECTION_FAILED") }
    }
    override val snapshot = machine.state
    private val startupFile = AtomicFile(File(context.noBackupFilesDir, "startup-diagnostics.json"))
    private var lastStartup = runCatching {
        if (startupFile.baseFile.length() > 16_384) "{}" else startupFile.openRead().use { JSONObject(it.readBytes().toString(Charsets.UTF_8)).toString() }
    }.getOrDefault("{}")
    val diagnosticState = kotlinx.coroutines.flow.MutableStateFlow(if (lastStartup == "{}") "{}" else JSONObject().put("LastStartup", JSONObject(lastStartup)).toString())
    @Synchronized fun recordStartup(generation: Long, raw: String, outcome: StartupOutcome = StartupOutcome.STARTING, failure: String? = null) {
        if (snapshot.value.generation != generation) return
        val source = if (TransportDiagnostics.parse(raw).root.field("StartupStage") == null) lastStartup else raw
        val report = startupEvidence(source, outcome, failure, System.currentTimeMillis())
        diagnosticState.value = runCatching { JSONObject(diagnosticState.value).put("LastStartup", JSONObject(report.toString())).toString() }.getOrDefault("{}")
        val before = TransportDiagnostics.parse(lastStartup).root
        lastStartup = report.toString()
        // Persist stage/count/outcome transitions, not every heartbeat or packet.
        if (listOf("StartupStage", "SelectedLines", "TotalLines", "Outcome", "StartupFailure").all { before[it] == report[it] }) return
        var stream: java.io.FileOutputStream? = null
        try {
            stream = startupFile.startWrite()
            stream.write(lastStartup.toByteArray(Charsets.UTF_8))
            startupFile.finishWrite(stream)
        } catch (_: Exception) { stream?.let { startupFile.failWrite(it) } }
    }
    override val diagnostics: kotlinx.coroutines.flow.StateFlow<String> = diagnosticState
    @Volatile var service: ItomVpnService? = null
    override fun start(profile: ServerProfile, revision: Long) {
        if (VpnService.prepare(context) != null) {
            machine.reject(profile.profileId, revision, profile.totalLines, "VPN_PERMISSION_REQUIRED"); return
        }
        diagnosticState.value = "{}"
        adminState.value = "{}"
        val generation = machine.begin(profile.profileId, revision, profile.accounts.count { it.enabled } * 2)
        recordStartup(generation, JSONObject().put("StartupStage", "PROFILE").put("SelectedLines", 0)
            .put("TotalLines", profile.accounts.count { it.enabled } * 2).put("ElapsedMS", 0).toString())
        try {
            context.startForegroundService(Intent(context, ItomVpnService::class.java)
                .putExtra("generation", generation).putExtra("profile", profile.profileId).putExtra("revision", revision))
        } catch (_: RuntimeException) {
            recordStartup(generation, "{}", StartupOutcome.FAILED, "VPN_SERVICE_START_FAILED")
            machine.fail(generation, "VPN_SERVICE_START_FAILED")
        }
    }
    override fun stop() {
        val generation = snapshot.value.generation
        recordStartup(generation, "{}", StartupOutcome.STOPPED)
        service?.requestStop(generation) ?: machine.stop(generation)
    }
}
