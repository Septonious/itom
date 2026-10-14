package org.itom.client.backend

import android.content.Context
import android.content.Intent
import android.net.VpnService
import org.itom.client.SessionBackend
import org.itom.client.VaultStore
import org.itom.domain.*

fun createBackend(context: Context, vault: VaultStore): SessionBackend = NativeBackend(context, vault)

class NativeBackend(private val context: Context, val vault: VaultStore) : SessionBackend {
    val machine = SessionMachine()
    override val snapshot = machine.state
    val diagnosticState = kotlinx.coroutines.flow.MutableStateFlow("{}")
    override val diagnostics: kotlinx.coroutines.flow.StateFlow<String> = diagnosticState
    @Volatile var service: ItomVpnService? = null
    override fun start(profile: ServerProfile, revision: Long) {
        if (VpnService.prepare(context) != null) {
            machine.reject(profile.profileId, revision, profile.totalLines, "VPN_PERMISSION_REQUIRED"); return
        }
        diagnosticState.value = "{}"
        val generation = machine.begin(profile.profileId, revision, profile.accounts.count { it.enabled } * 2)
        try {
            context.startForegroundService(Intent(context, ItomVpnService::class.java)
                .putExtra("generation", generation).putExtra("profile", profile.profileId).putExtra("revision", revision))
        } catch (_: RuntimeException) { machine.fail(generation, "VPN_SERVICE_START_FAILED") }
    }
    override fun stop() {
        val generation = snapshot.value.generation
        service?.requestStop(generation) ?: machine.stop(generation)
    }
}
