package org.itom.client

import android.content.Intent
import android.os.Build
import android.provider.Settings
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.core.app.NotificationManagerCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import org.itom.domain.*

@Composable fun SettingsScreen(backend: SessionBackend, session: SessionSnapshot, onMessage: (String) -> Unit, navigate: (Page) -> Unit) {
    val raw by backend.admin.collectAsStateWithLifecycle()
    val admin = remember(raw) { AdminCodec.view(raw) }
    var selected by remember { mutableIntStateOf(0) }
    LaunchedEffect(admin.authorized) { if (!admin.authorized) selected = 0 }
    Column(Modifier.fillMaxSize()) {
        if (admin.authorized) PrimaryTabRow(selectedTabIndex = selected, containerColor = androidx.compose.ui.graphics.Color.Transparent) {
            Tab(selected = selected == 0, onClick = { selected = 0 }, text = { Text("App settings") })
            Tab(selected = selected == 1, onClick = { selected = 1 }, text = { Text("Server admin") })
        }
        if (selected == 1 && admin.authorized) AdminScreen(backend, session, onMessage)
        else PageColumn {
            NavigationRow(Glyph.NETWORK, "VPN networks", "Choose Wi-Fi, mobile, or both") { navigate(Page.NETWORKS) }
            NavigationRow(Glyph.NETWORK, "DNS", "Direct resolvers and IMAP fallback") { navigate(Page.DNS) }
            NavigationRow(Glyph.NETWORK, "SOCKS5", "Port and authentication") { navigate(Page.SOCKS) }
            NavigationRow(Glyph.BELL, "Notifications", "Android permission and notification settings") { navigate(Page.NOTIFICATION) }
            NavigationRow(Glyph.CHART, "Diagnostics", "Account metrics and full diagnostics export") { navigate(Page.DIAGNOSTICS) }
            if (!admin.authorized) SmallNote("Server administration appears here when the connected server grants this client admin access.")
        }
    }
}

@Composable fun NetworkScreen(config: PublicConfig, busy: Boolean, onNetworksChanged: (NetworkPolicy) -> Unit) {
    PageColumn {
        SmallNote("VPN stops when you switch to an unselected network type. Select both to keep VPN active across Wi-Fi and mobile changes.")
        InfoCard(Modifier.fillMaxWidth()) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                Text("Wi-Fi", Modifier.weight(1f))
                ItomSwitch(config.settings.networks.wifi, { onNetworksChanged(config.settings.networks.copy(wifi = it)) },
                    enabled = !busy && !config.loading && (config.settings.networks.mobile || !config.settings.networks.wifi))
            }
            Row(Modifier.fillMaxWidth(), verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                Text("Mobile", Modifier.weight(1f))
                ItomSwitch(config.settings.networks.mobile, { onNetworksChanged(config.settings.networks.copy(mobile = it)) },
                    enabled = !busy && !config.loading && (config.settings.networks.wifi || !config.settings.networks.mobile))
            }
        }
        SmallNote("At least one network type must remain selected.")
    }
}

@Composable fun NotificationScreen() {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    var enabled by remember(context) { mutableStateOf(NotificationManagerCompat.from(context).areNotificationsEnabled()) }
    DisposableEffect(context, lifecycle) {
        val observer = LifecycleEventObserver { _, event ->
            if (event == Lifecycle.Event.ON_RESUME) enabled = NotificationManagerCompat.from(context).areNotificationsEnabled()
        }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }
    PageColumn {
        InfoCard(Modifier.fillMaxWidth()) {
            Text("ITOM notifications", style = MaterialTheme.typography.titleMedium)
            Text(if (enabled) "Notifications are allowed" else "Notifications are disabled in Android")
            SmallNote("The VPN notification shows current speeds and RTT.")
        }
        NavigationRow(Glyph.BELL, "Android settings", "Permission and notification display") { context.startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName)) }
    }
}

@Composable fun DiagnosticsScreen(config: PublicConfig, session: SessionSnapshot, diagnostics: String, onDiagnosticsChanged: (Boolean) -> Unit, onMessage: (String) -> Unit) {
    val context = LocalContext.current; val scope = rememberCoroutineScope()
    fun currentReport(): String {
        return Codec.json.encodeToString(JsonObject.serializer(), buildJsonObject {
                put("schema_version", 5); put("build", BuildConfig.VERSION_NAME); put("ui_baseline", BuildConfig.UI_BASELINE)
                put("android_api", Build.VERSION.SDK_INT); put("phase", session.phase.name); put("generation", session.generation)
                put("configuration_revision", config.revision); put("profile_count", config.profiles.size)
                put("vpn_interface_up", session.vpnInterfaceUp); put("down_bps", session.metrics.downBps); put("up_bps", session.metrics.upBps)
                put("rtt_ms", session.metrics.rttMs?.let(::JsonPrimitive) ?: JsonNull)
                put("tcp", session.metrics.tcp); put("active_lines", session.metrics.activeLines); put("total_lines", session.metrics.totalLines)
                put("error_code", session.error?.let(::JsonPrimitive) ?: JsonNull)
                put("transport", runCatching { Codec.json.parseToJsonElement(diagnostics) }.getOrDefault(JsonObject(emptyMap())))
            })
    }
    var zipReport by rememberSaveable { mutableStateOf<String?>(null) }
    var zipProfile by rememberSaveable { mutableStateOf<String?>(null) }
    val exportZip = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/zip")) { uri ->
        val text = zipReport; val profile = zipProfile; zipReport = null; zipProfile = null
        if (uri != null && text != null) scope.launch {
            try {
                withContext(Dispatchers.IO) {
                    context.contentResolver.openOutputStream(uri, "wt")?.use { output ->
                        exportTransportTrace(context.noBackupFilesDir, profile, text, output)
                    } ?: throw DomainError("EXPORT_FAILED")
                }
                onMessage("Diagnostics archive saved.")
            } catch (e: CancellationException) { throw e } catch (_: Exception) { onMessage("Could not save the archive.") }
        }
    }
    PageColumn {
        InfoCard(Modifier.fillMaxWidth()) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text("Logs and tracing", style = MaterialTheme.typography.titleMedium)
                    SmallNote(if (config.settings.diagnosticsEnabled) "Recording transport events." else "Tracing is off. The last connection attempt is still saved for diagnostics.")
                }
                ItomSwitch(config.settings.diagnosticsEnabled, onDiagnosticsChanged)
            }
        }
        StatusLabel(phaseName(session.phase), phaseColor(session.phase))
        val view = remember(diagnostics) { TransportDiagnostics.parse(diagnostics) }
        if (view.lines.isEmpty()) SmallNote("Account metrics appear when you connect.")
        view.lines.forEach { line -> TransportLine(config, session, line) }
        SmallNote("The full archive includes a state snapshot and available trace files. Turn on tracing to record transport events.")
        GradientButton("Export full diagnostics", onClick = {
            zipReport = currentReport()
            zipProfile = session.profileId ?: config.selectedProfileId
            exportZip.launch("ITOM_diagnostics_full.zip")
        }, modifier = Modifier.fillMaxWidth(), enabled = !config.loading, glyph = Glyph.DOWN)
    }
}
