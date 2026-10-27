package org.itom.client

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.SystemBarStyle
import androidx.activity.compose.BackHandler
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import org.itom.domain.*

class MainActivity : ComponentActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        enableEdgeToEdge(statusBarStyle = SystemBarStyle.dark(android.graphics.Color.TRANSPARENT),
            navigationBarStyle = SystemBarStyle.dark(android.graphics.Color.TRANSPARENT))
        val graph = (application as ItomApplication).graph
        setContent { ItomApp(graph) }
    }
}

enum class Page(val title: String, val root: String) {
    HOME("ITOM", "home"), PROFILES("Profiles", "profiles"), RULES("Rules", "rules"), SETTINGS("Settings", "settings"),
    APPS("Applications", "rules"), IMPORT("Add server", "profiles"), EDIT_PROFILE("Profile editor", "profiles"), DASHBOARD("Statistics", "home"),
    SOCKS("SOCKS5", "settings"), NETWORKS("VPN networks", "settings"), DNS("DNS", "settings"), NOTIFICATION("Notification", "settings"), DIAGNOSTICS("Diagnostics", "settings")
}

fun errorText(code: String) = when (code) {
    "DNS_ADDRESS" -> "Enter a valid resolver IP address. The backup may be empty."
    "DNS_BACKUP_REQUIRED" -> "Set a backup resolver before enabling IMAP fallback."
    "DNS_DUPLICATE" -> "Choose different primary and backup resolvers."
    "SOCKS_PORT" -> "SOCKS port must be a number from 1024 to 65535."
    "SOCKS_CREDENTIALS" -> "Username and password must contain 1 to 255 UTF-8 bytes with no line breaks."
    "SOCKS_EDIT_CONFLICT" -> "Settings changed. Reopen SOCKS5 before saving."
    "EMPTY_INCLUDE" -> "Select at least one application."
    "APP_NOT_INSTALLED" -> "Some selected applications were removed. Refresh the list and selection."
    "SHARED_UID_PARTIAL_SELECTION" -> "Applications sharing an Android UID must be selected together."
    "DUPLICATE_SERVER" -> "A profile for this server already exists."
    "PROFILE_MISSING" -> "This profile was removed or is no longer available."
    "PROFILE_EDIT_CONFLICT" -> "The profile changed after the editor opened. Reload it before saving."
    "PROFILE_NAME", "ACCOUNT_NAME" -> "Names must contain 1 to 80 characters with no control characters."
    "SERVER_ID", "PAIR_ID" -> "Server ID and Pair ID must contain 32 hexadecimal characters."
    "USER_ID" -> "User ID must contain exactly 8 digits and must not start with zero."
    "PROVIDER_ID" -> "Provider ID: up to 64 lowercase ASCII letters, digits, hyphens or underscores."
    "FOLDER_NAMESPACE" -> "Mailbox namespace: 2 to 128 ASCII letters, digits, dots, hyphens, underscores or slashes; empty segments and .. are not allowed."
    "PSK_FORMAT" -> "The key must contain 32 bytes encoded in Base64."
    "ACCOUNT_COUNT" -> "A profile needs at least one account, with at least one enabled."
    "ACCOUNT_ID" -> "Account ID: 1 to 64 ASCII letters, digits, hyphens or underscores."
    "DUPLICATE_ACCOUNT" -> "Account IDs must be unique within a profile."
    "IMAP_PORT" -> "IMAP port must be a number from 1 to 65535."
    "IMAP_HOST" -> "Enter the IMAP server hostname or IP without a scheme, path or port."
    "CREDENTIAL_FORMAT" -> "Check account usernames and passwords. Both fields are required; line breaks are not allowed."
    "MIXED_PROVIDERS" -> "All accounts must use the same provider."
    "INVITATION_EXPIRED" -> "The invitation has expired."
    "VAULT_UNAVAILABLE" -> "Secure storage is unavailable. Your data is retained; retry after unlocking the device."
    "VAULT_WRITE_FAILED" -> "Saving could not be confirmed. Check available storage and tap Retry to reload."
    "VPN_PERMISSION_REQUIRED", "VPN_PERMISSION_REVOKED" -> "Allow ITOM to create a VPN connection."
    "NO_NETWORK", "UNDERLYING_DNS_MISSING" -> "No available network or DNS servers."
    "UNDERLYING_NETWORK_FAILED" -> "Android could not use the selected network. Check the connection and reconnect."
    "IMAP_MAILBOX_STATE_CHANGED" -> "The mail server changed mailbox state. ITOM is reconnecting."
    "SOCKS_PORT_IN_USE" -> "The SOCKS port is in use. Choose another port in settings."
    "TRANSPORT_TIMEOUT" -> "The server connection could not be restored. VPN stopped."
    "V2_STARTUP_PROFILE", "NATIVE_CONFIG" -> "The transport could not read this profile. Update the app and check the server invitation."
    "V2_STARTUP_WALLET" -> "The transport could not open its saved accounting data. Keep app data and export diagnostics."
    "V2_STARTUP_IMAP" -> "IMAP connection setup failed. Check account access and export app and server diagnostics."
    "V2_PROVIDER_UIDPLUS_REQUIRED", "V2_PROVIDER_PERMANENT_KEYWORDS_REQUIRED" -> "The mail provider does not support a required transport mailbox feature. Export diagnostics for the exact capability."
    "V2_STARTUP_RENDEZVOUS" -> "The app could not read a matching server offer through IMAP. Export app and server diagnostics."
    "V2_STARTUP_REQUEST" -> "The app found the server offer but could not send its session request. Export app and server diagnostics."
    "V2_STARTUP_INVITATION" -> "The app found the server but did not receive a session response. Export app and server diagnostics."
    "V2_STARTUP_NOISE", "V2_STARTUP_ROSTER" -> "The secure session could not be established. Check the server profile and export diagnostics."
    "V2_STARTUP_RUNTIME", "V2_STARTUP_PERMISSION" -> "The session could not obtain ready mail lines. Export app and server diagnostics."
    "TRANSPORT_FAILED", "VPN_SERVICE_START_FAILED" -> "Could not start the transport. Check the server and profile."
    "NATIVE_CLOSE_FAILED" -> "VPN stopped after a transport cleanup failure. Try connecting again."
    "INVALID_JSON", "DUPLICATE_JSON_KEY", "INVITATION_FORMAT", "INVITATION_VERSION" -> "Invalid invitation format."
    "DOCUMENT_TOO_LARGE" -> "The file exceeds the size limit."
    "QR_NOT_FOUND" -> "No invitation QR code was found in the image."
    else -> "Operation failed: $code"
}

@Composable fun ItomApp(graph: AppGraph) {
    val config by graph.vault.state.collectAsStateWithLifecycle()
    val session by graph.backend.snapshot.collectAsStateWithLifecycle()
    val diagnostics by graph.backend.diagnostics.collectAsStateWithLifecycle()
    var page by rememberSaveable { mutableStateOf(Page.HOME) }
    var editingProfileId by rememberSaveable { mutableStateOf<String?>(null) }
    var orbit by rememberSaveable { mutableFloatStateOf(35f) }
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    var busy by remember { mutableStateOf(false) }
    fun message(text: String) { scope.launch { snackbar.showSnackbar(text) } }
    fun perform(block: suspend () -> Unit) {
        if (busy) return
        busy = true
        scope.launch {
            try { block() } catch (e: CancellationException) { throw e }
            catch (e: DomainError) { snackbar.showSnackbar(errorText(e.code)) }
            catch (_: Exception) { snackbar.showSnackbar("The operation failed. Please try again.") }
            finally { busy = false }
        }
    }
    val consent = androidx.activity.compose.rememberLauncherForActivityResult(androidx.activity.result.contract.ActivityResultContracts.StartActivityForResult()) { result ->
        if (result.resultCode == android.app.Activity.RESULT_OK) {
            val current = graph.vault.state.value
            current.selected?.let { graph.backend.start(it, current.revision) }
        }
    }
    fun requestVPN() {
        val current = graph.vault.state.value
        val selected = current.selected ?: return
        val permission = android.net.VpnService.prepare(graph.context)
        if (permission != null) consent.launch(permission) else graph.backend.start(selected, current.revision)
    }
    val notifications = androidx.activity.compose.rememberLauncherForActivityResult(androidx.activity.result.contract.ActivityResultContracts.RequestPermission()) { requestVPN() }
    fun startSelected() {
        val current = graph.vault.state.value
        val profile = current.selected
        if (current.loading || current.error != null) { message(errorText("VAULT_UNAVAILABLE")); return }
        if (profile == null) { page = Page.PROFILES; return }
        if (profile.profileId in current.incompleteProfileIds) {
            editingProfileId = profile.profileId; page = Page.EDIT_PROFILE
            message("Enter the encryption key and credentials for every IMAP account."); return
        }
        if (android.os.Build.VERSION.SDK_INT >= 33 && androidx.core.content.ContextCompat.checkSelfPermission(graph.context, android.Manifest.permission.POST_NOTIFICATIONS) != android.content.pm.PackageManager.PERMISSION_GRANTED) {
            notifications.launch(android.Manifest.permission.POST_NOTIFICATIONS)
            return
        }
        requestVPN()
    }
    fun restartIfActive() {
        if (graph.backend.snapshot.value.phase in listOf(Phase.CONNECTING, Phase.CONNECTED)) startSelected()
    }
    fun toggle() {
        if (graph.backend.snapshot.value.phase in listOf(Phase.CONNECTING, Phase.CONNECTED)) graph.backend.stop()
        else startSelected()
    }
    fun back() { page = when (page.root) { "rules" -> Page.RULES; "profiles" -> Page.PROFILES; "settings" -> Page.SETTINGS; else -> Page.HOME } }
    BackHandler(page !in listOf(Page.HOME, Page.PROFILES, Page.RULES, Page.SETTINGS)) { back() }
    ItomTheme {
      ItomBackdrop {
        Scaffold(containerColor = Color.Transparent, contentColor = Ink, contentWindowInsets = WindowInsets.safeDrawing, topBar = {
            if (page !in listOf(Page.APPS, Page.EDIT_PROFILE)) ItomTopBar(page, ::back, session)
        }, bottomBar = {
            // Editors own their header, Back handling and save bar.
            if (page !in listOf(Page.APPS, Page.EDIT_PROFILE)) ItomBottomBar(page) { page = it }
        }, snackbarHost = { SnackbarHost(snackbar) }) { padding ->
            Box(Modifier.fillMaxSize().padding(padding).consumeWindowInsets(padding).imePadding()) {
                when {
                    config.loading -> CircularProgressIndicator(Modifier.padding(24.dp))
                    config.error != null -> PageColumn { Text(errorText(config.error!!)); GradientButton("Retry", { scope.launch { graph.vault.load() } }) }
                    else -> when (page) {
                        Page.HOME -> HomeScreen(config, session, { orbit }, { orbit = it }, ::toggle, { page = Page.PROFILES })
                        Page.PROFILES -> ProfilesScreen(config, busy, onSelect = { id -> perform { graph.vault.select(id); restartIfActive() } },
                            onEdit = { id -> editingProfileId = id; page = Page.EDIT_PROFILE }, onDelete = { id -> perform {
                                if (graph.backend.snapshot.value.profileId == id) graph.backend.stop()
                                graph.vault.remove(id)
                                // Start may have been tapped while the disk transaction was in flight.
                                if (graph.backend.snapshot.value.profileId == id) graph.backend.stop()
                            } }, onImport = { page = Page.IMPORT },
                            onMessage = ::message)
                        Page.RULES -> RulesScreen(config.settings.routing, config.settings.appRouting, busy, { page = Page.APPS }) { action, entries -> perform { graph.vault.rules(action, entries); restartIfActive(); message("${routeName(action)} rules saved.") } }
                        Page.APPS -> AppsScreen(config.settings.appRouting, graph.catalog, busy, ::back) { policy, _ -> perform { graph.vault.apps(policy, graph.catalog.load()); restartIfActive(); page = Page.RULES } }
                        Page.IMPORT -> ImportScreen(busy, ::message) { invitation -> perform { graph.vault.importInvitation(invitation); restartIfActive(); page = Page.PROFILES } }
                        Page.EDIT_PROFILE -> {
                            val id = editingProfileId
                            if (id == null) LaunchedEffect(Unit) { page = Page.PROFILES }
                            else ProfileEditorScreen(id, graph, close = { editingProfileId = null; page = Page.PROFILES }, saved = { restartFailed ->
                                editingProfileId = null; page = Page.PROFILES
                                message(if (restartFailed) "Profile saved. Reconnect to apply it." else "Profile saved.")
                            })
                        }
                        Page.DASHBOARD, Page.DIAGNOSTICS -> DiagnosticsScreen(config, session, diagnostics, { enabled -> perform { graph.vault.diagnostics(enabled); restartIfActive(); message(if (enabled) "Logs and tracing enabled." else "Logs and tracing disabled.") } }, ::message)
                        Page.SETTINGS -> SettingsScreen(graph.backend, session, ::message) { page = it }
                        Page.NETWORKS -> NetworkScreen(config, busy) { policy -> perform { graph.vault.networks(policy); message("Network selection saved.") } }
                        Page.DNS -> DNSScreen(config, busy) { value -> perform { graph.vault.dns(value); restartIfActive(); page = Page.SETTINGS; message("DNS settings saved.") } }
                        Page.SOCKS -> SocksScreen(graph, onSaved = { restartIfActive(); page = Page.SETTINGS; message("SOCKS5 settings saved.") })
                        Page.NOTIFICATION -> NotificationScreen()
                    }
                }
            }
        }
      }
    }
}
