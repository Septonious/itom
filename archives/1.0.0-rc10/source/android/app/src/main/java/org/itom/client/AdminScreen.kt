package org.itom.client

import android.os.SystemClock
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.ui.draw.clip
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import kotlinx.coroutines.*
import kotlinx.serialization.json.*
import org.itom.domain.*
import java.util.Base64
import java.util.UUID

private data class AdminField(val key: String, val label: String, val initial: String = "", val secret: Boolean = false, val multiline: Boolean = false, val required: Boolean = true, val choices: List<Pair<String, String>> = emptyList())
private data class AdminForm(val title: String, val fields: List<AdminField>, val messagePreview: String? = null, val submit: (Map<String, String>) -> Unit)
private data class AdminChange(val title: String, val detail: String, val request: JsonObject)
private enum class AdminSection(val label: String, val description: String, val glyph: Glyph) {
    OVERVIEW("Overview", "Server status and live traffic", Glyph.HOME),
    CLIENTS("Clients", "Access and invitations", Glyph.APPS),
    ACCOUNTS("Mail accounts", "Credentials and connectivity", Glyph.MAIL),
    MESSAGES("Messages", "Global and client notices", Glyph.BELL),
    TRANSPORT("Transport", "Mailbox layout and resource limits", Glyph.NETWORK),
    DIAGNOSTICS("Diagnostics", "Logs, tracing and export", Glyph.CHART),
    SERVICE("Service", "Start, restart and recovery", Glyph.SETTINGS)
}

@Composable fun AdminScreen(backend: SessionBackend, session: SessionSnapshot, onMessage: (String) -> Unit) {
    val raw by backend.admin.collectAsStateWithLifecycle()
    val view = remember(raw) { AdminCodec.view(raw) }
    val status = view.status
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var section by rememberSaveable { mutableStateOf(AdminSection.OVERVIEW) }
    var sectionsOpen by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    var form by remember { mutableStateOf<AdminForm?>(null) }
    var confirmation by remember { mutableStateOf<AdminChange?>(null) }
    var output by remember { mutableStateOf("") }
    var invitation by remember { mutableStateOf("") }
    var outputKind by remember { mutableStateOf("logs") }
    var live by remember { mutableStateOf(false) }
    var checkID by remember { mutableStateOf("") }
    var exportID by remember { mutableStateOf("") }
    var exportTotal by remember { mutableLongStateOf(0) }
    var exportGeneration by remember { mutableLongStateOf(0) }
    var exportProgress by remember { mutableFloatStateOf(0f) }
    val pending = view.pending
    val trial = pending?.active == true
    val canChange = view.authorized && status?.watchdog_available == true && session.phase == Phase.CONNECTED && !trial && !busy
    fun request(operation: String, fields: JsonObjectBuilder.() -> Unit = {}) = buildJsonObject { put("operation", operation); fields() }
    suspend fun rpc(q: JsonObject): AdminReply {
        val reply = AdminCodec.reply(backend.adminCall(q.toString()))
        if (!reply.ok) throw DomainError(reply.error ?: "ADMIN_REQUEST_FAILED")
        return reply
    }
    fun call(q: JsonObject, done: (AdminReply) -> Unit = {}) {
        if (busy) return
        busy = true
        scope.launch {
            try { val result = rpc(q); done(result) }
            catch (e: CancellationException) { throw e }
            catch (e: DomainError) { onMessage(adminError(e.code)) }
            catch (_: Exception) { onMessage("Server connection interrupted. Check the change status before retrying.") }
            finally { busy = false }
        }
    }
    fun change(title: String, operation: String, detail: String = "The server will test this change and restore the previous configuration if this client cannot reconnect within 60 seconds.", fields: JsonObjectBuilder.() -> Unit = {}) {
        confirmation = AdminChange(title, detail, request(operation) { put("id", UUID.randomUUID().toString().replace("-", "")); put("revision", status?.revision.orEmpty()); fields() })
    }
    fun textForm(title: String, label: String, initial: String = "", secret: Boolean = false, submit: (String) -> Unit) {
        form = AdminForm(title, listOf(AdminField("value", label, initial, secret))) { submit(it.getValue("value")) }
    }
    val export = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/zip")) { uri ->
        if (uri != null && exportID.isNotEmpty()) {
            val id = exportID; val total = exportTotal; val generation = exportGeneration
            busy = true
            scope.launch {
                try {
                    withContext(Dispatchers.IO) {
                        require(total in 1..(64L shl 20))
                        context.contentResolver.openOutputStream(uri, "wt")?.use { out ->
                            var offset = 0L
                            while (offset < total) {
                                ensureActive()
                                if (backend.snapshot.value.generation != generation) throw DomainError("ADMIN_CONNECTION_FAILED")
                                val chunk = rpc(request("diagnostics_chunk") { put("id", id); put("offset", offset) })
                                val bytes = Base64.getDecoder().decode(chunk.data)
                                require(chunk.total == total && bytes.isNotEmpty() && bytes.size <= 128 * 1024 && offset + bytes.size <= total)
                                out.write(bytes); offset += bytes.size
                                withContext(Dispatchers.Main) { exportProgress = offset.toFloat() / total }
                            }
                        } ?: throw DomainError("EXPORT_FAILED")
                    }
                    onMessage("Server diagnostics saved.")
                } catch (e: CancellationException) { throw e }
                catch (_: Exception) { onMessage("Export incomplete. Retry when connected; the partial file may need to be removed.") }
                finally { busy = false; exportID = ""; exportProgress = 0f }
            }
        }
    }
    val saveInvitation = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("text/plain")) { uri ->
        val text = invitation
        if (uri != null && text.isNotEmpty()) scope.launch {
            try {
                withContext(Dispatchers.IO) { context.contentResolver.openOutputStream(uri, "wt")?.use { it.write(text.toByteArray(Charsets.UTF_8)) } ?: throw DomainError("EXPORT_FAILED") }
                onMessage("Client invitation saved.")
                form = null; invitation = ""
            } catch (e: CancellationException) { throw e } catch (_: Exception) { onMessage("Could not save the invitation.") }
        }
    }
    LaunchedEffect(section, session.generation) {
        while (isActive) {
            try { rpc(request("status")) } catch (e: CancellationException) { throw e } catch (_: Exception) { /* Keep the last verified snapshot while reconnecting. */ }
            delay(if (section == AdminSection.OVERVIEW) 3000 else 10000)
        }
    }
    LaunchedEffect(section, live, outputKind, session.generation) {
        if (section != AdminSection.DIAGNOSTICS || !live) return@LaunchedEffect
        while (isActive) {
            try { output = rpc(request(outputKind)).text }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { output = "Waiting for the server connection…" }
            delay(5000)
        }
    }
    LaunchedEffect(checkID, session.generation) {
        if (checkID.isEmpty()) return@LaunchedEffect
        while (isActive) {
            try {
                val result = rpc(request("check_status") { put("id", checkID) })
                output = result.text
                if (result.total == 1L) { checkID = ""; break }
            } catch (e: CancellationException) { throw e }
            catch (_: Exception) { output = "Account checks interrupted. Reconnect to retry."; checkID = ""; break }
            delay(2000)
        }
    }
    Column(Modifier.fillMaxSize()) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 12.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            IconBadge(section.glyph)
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(section.label, style = MaterialTheme.typography.titleLarge)
                SmallNote(section.description)
            }
            Box {
                OutlinedButton(onClick = { sectionsOpen = true }, colors = ButtonDefaults.outlinedButtonColors(containerColor = Panel, contentColor = Blue)) { Text("Sections") }
                DropdownMenu(
                    expanded = sectionsOpen,
                    onDismissRequest = { sectionsOpen = false },
                    modifier = Modifier.background(Panel, RoundedCornerShape(18.dp))
                        .border(1.dp, Outline.copy(alpha = .6f), RoundedCornerShape(18.dp))
                ) {
                    AdminSection.entries.forEach { item ->
                        val selected = item == section
                        DropdownMenuItem(
                            text = { Text(item.label, color = if (selected) Blue else Ink) },
                            leadingIcon = { AppIcon(item.glyph, Modifier.size(20.dp), if (selected) Blue else Muted) },
                            modifier = Modifier.padding(horizontal = 6.dp).clip(RoundedCornerShape(10.dp))
                                .background(if (selected) Blue.copy(alpha = .16f) else Panel),
                            colors = MenuDefaults.itemColors(textColor = Ink, leadingIconColor = Muted),
                            onClick = { sectionsOpen = false; section = item; output = ""; live = false }
                        )
                    }
                }
            }
        }
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 4.dp, bottom = 24.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
            if (trial) item {
                AdminTrialCard(pending, enabled = !busy && pending.owner == status?.current_uid, undo = { call(request("undo") { put("id", pending.id) }) })
            }
            if (pending != null && !pending.active) item {
                StatusLabel(when (pending.state) { "committed" -> "Change verified and saved"; "rolled_back" -> "Previous configuration restored"; else -> "Change canceled" }, if (pending.state == "committed") Green else Yellow)
            }
            if (status == null) item { InfoCard(Modifier.fillMaxWidth()) { Text("Connecting to the server manager", style = MaterialTheme.typography.titleMedium); SmallNote("Keep the VPN connected. Admin access is verified by the server.") } }
            if (status != null) when (section) {
                AdminSection.OVERVIEW -> {
                    item { InfoCard(Modifier.fillMaxWidth(), accent = Green) { StatusLabel("ADMIN · ${status.current_uid}", Green); Text(status.provider, style = MaterialTheme.typography.headlineSmall); SmallNote("Service ${status.service} · ${status.accounts.size} accounts · ${status.users.size}/2 clients"); SmallNote(if (status.watchdog_available) "Automatic rollback protection is available." else "Changes require the installed Linux service and an independent rollback watchdog.") } }
                    item { Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) { AdminStat("Accounts", status.accounts.size.toString(), Modifier.weight(1f)); AdminStat("Clients", status.users.size.toString(), Modifier.weight(1f)) } }
                    status.traffic?.let { traffic -> item { AdminTraffic(traffic, status.users) } }
                    item { AdminHeading("Access & communication", "Manage who connects and what clients see.") }
                    item { AdminCategoryRow(AdminSection.CLIENTS, AdminSection.ACCOUNTS) { section = it } }
                    item { NavigationRow(Glyph.BELL, "Server messages", "Global notice and individual client messages") { section = AdminSection.MESSAGES } }
                    item { AdminHeading("System", "Observe the server and manage its configuration.") }
                    item { AdminCategoryRow(AdminSection.TRANSPORT, AdminSection.DIAGNOSTICS) { section = it } }
                    item { NavigationRow(Glyph.SETTINGS, "Service control", "Service state and protected recovery actions") { section = AdminSection.SERVICE } }
                    item { TextButton(onClick = { call(request("status")) }, enabled = !busy) { Text("Refresh server status") } }
                }
                AdminSection.CLIENTS -> {
                    item { AdminHeading("Clients", "Client identities and mailbox journals are preserved when a registration is removed.") }
                    items(status.users, key = { it.id }) { user -> InfoCard(Modifier.fillMaxWidth()) {
                        Row(Modifier.fillMaxWidth()) { Text(user.name, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium); StatusLabel(if (user.enabled) "ENABLED" else "DISABLED", if (user.enabled) Green else Muted) }
                        SmallNote("UID ${user.id}${if (user.id == status.current_uid) " · This client" else ""}")
                        Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState())) {
                            TextButton(onClick = { textForm("Rename client", "Name", user.name) { value -> change("Rename ${user.name}", "client_rename") { put("uid", user.id); put("name", value) } } }, enabled = canChange) { Text("Rename") }
                            TextButton(onClick = { change(if (user.enabled) "Disable client" else "Enable client", "client_enabled") { put("uid", user.id); put("enabled", !user.enabled) } }, enabled = canChange) { Text(if (user.enabled) "Disable" else "Enable") }
                            TextButton(onClick = { change("Remove ${user.name}", "client_remove") { put("uid", user.id) } }, enabled = canChange) { Text("Remove", color = if (canChange) Red else Muted) }
                        }
                        OutlinedButton(onClick = { call(request("profile") { put("uid", user.id) }) { reply -> form = AdminForm("Android invitation", emptyList(), submit = {}) ; invitation = reply.text } }, enabled = !busy && user.enabled) { Text("Export Android invitation") }
                    } }
                    item { GradientButton("Add client", { textForm("Add client", "Client name") { value -> change("Add client", "client_add") { put("name", value) } } }, Modifier.fillMaxWidth(), enabled = canChange && status.users.size < 2, glyph = Glyph.PLUS) }
                }
                AdminSection.ACCOUNTS -> {
                    item { AdminHeading("Mail accounts", "Update client invitations after changing account credentials or membership.") }
                    items(status.accounts, key = { it.id }) { account -> InfoCard(Modifier.fillMaxWidth()) {
                        Text(account.username, style = MaterialTheme.typography.titleMedium)
                        SmallNote("${account.id} · ${account.host}:${account.port} · ${if (account.tls == "tls") "TLS" else "STARTTLS"}")
                        Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState())) {
                            TextButton(onClick = { call(request("check") { put("account_id", account.id) }) { checkID = it.id } }, enabled = !busy && checkID.isEmpty()) { Text("Check") }
                            TextButton(onClick = { textForm("Change account password", "Application password", secret = true) { value -> change("Change account password", "account_password") { put("account_id", account.id); put("password", value) } } }, enabled = canChange) { Text("Password") }
                            TextButton(onClick = { form = AdminForm("Server IMAP IP pin", listOf(AdminField("ip", "IPv4/IPv6; blank uses DNS", account.dial_ip, required = false))) { fields ->
                                val ip = fields.getValue("ip").trim()
                                if (!Validation.validDialIP(ip)) onMessage("Enter a literal IPv4 or IPv6 address, or leave blank for DNS.")
                                else change("Change server IMAP IP pin", "account_pin") { put("account_id", account.id); put("dial_ip", ip) }
                            } }, enabled = canChange) { Text("IP pin") }
                            TextButton(onClick = { change("Remove account", "account_remove") { put("account_id", account.id) } }, enabled = canChange) { Text("Remove", color = if (canChange) Red else Muted) }
                        }
                    } }
                    item { OutlinedButton(enabled = canChange && status.accounts.isNotEmpty(), onClick = {
                        form = AdminForm("Server IMAP IP pin for all accounts", listOf(AdminField("ip", "IPv4/IPv6; blank uses DNS", required = false))) { fields ->
                            val ip = fields.getValue("ip").trim()
                            if (!Validation.validDialIP(ip)) onMessage("Enter a literal IP address or leave blank for DNS.")
                            else change("Change all server IMAP IP pins", "account_pin") { put("account_id", "*"); put("dial_ip", ip) }
                        }
                    }) { Text("Set IP pin for all server accounts") } }
                    item { GradientButton("Add mail account", {
                        val first = status.accounts.firstOrNull()
                        form = AdminForm("Add mail account", listOf(AdminField("host", "IMAP hostname", first?.host ?: ""), AdminField("dial_ip", "Pinned IMAP IP (optional)", first?.dial_ip ?: "", required = false), AdminField("port", "Port", (first?.port ?: 993).toString()), AdminField("tls", "Connection security", first?.tls ?: "tls", choices = listOf("tls" to "TLS", "starttls" to "STARTTLS")), AdminField("username", "Username"), AdminField("password", "Application password", secret = true))) { fields ->
                            val port = fields.getValue("port").toIntOrNull()
                            if (port == null || port !in 1..65535 || fields["tls"] !in setOf("tls", "starttls")) onMessage("Enter a valid port and select tls or starttls.")
                            else change("Add mail account", "account_add") { put("mail", buildJsonObject { fields.forEach { (key, value) -> if (key == "port") put(key, port) else put(key, value) } }) }
                        }
                    }, Modifier.fillMaxWidth(), enabled = canChange, glyph = Glyph.PLUS) }
                    item { OutlinedButton(onClick = { call(request("check")) { checkID = it.id } }, enabled = !busy && checkID.isEmpty(), modifier = Modifier.fillMaxWidth()) { Text("Check all accounts") } }
                    if (output.isNotEmpty()) item { AdminOutput(output) }
                }
                AdminSection.MESSAGES -> {
                    item { AdminHeading("Server messages", "Individual messages override the global notice. Every global save clears all individual messages.") }
                    item { AdminMessageCard("Global message", status.global_message, "Shown to clients without an individual message", canChange) { value -> change("Update global message", "global_message", "This clears all individual messages. The previous notice and overrides remain protected by the connection rollback trial.") { put("text", value) } } }
                    items(status.users, key = { it.id }) { user -> AdminMessageCard("${user.name} · ${user.id}", user.message, "Leave empty to use the global message", canChange, status.global_message) { value -> change("Update individual message", "client_message") { put("uid", user.id); put("text", value) } } }
                    item { SmallNote("Colors: &0–&f · &l bold · &o italic · &n underline · &m strike · &r reset · && literal &") }
                }
                AdminSection.DIAGNOSTICS -> {
                    item { AdminHeading("Observe & troubleshoot", "Inspect live activity or save a complete server capture.") }
                    item { InfoCard(Modifier.fillMaxWidth()) { Row(Modifier.fillMaxWidth(), verticalAlignment = androidx.compose.ui.Alignment.CenterVertically) { Column(Modifier.weight(1f)) { Text("Server tracing", style = MaterialTheme.typography.titleMedium); SmallNote("Record transport events. Changes restart the server.") }; ItomSwitch(status.diagnostics == "on", { enabled -> change("Change server tracing", "diagnostics") { put("diagnostics", if (enabled) "on" else "off") } }, enabled = canChange) } } }
                    item { Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) { FilterChip(outputKind == "logs", { outputKind = "logs"; call(request("logs")) { output = it.text } }, label = { Text("Logs") }); FilterChip(outputKind == "trace", { outputKind = "trace"; call(request("trace")) { output = it.text } }, label = { Text("Packet trace") }); FilterChip(live, { live = !live }, label = { Text("Live") }) } }
                    if (output.isNotEmpty()) item { AdminOutput(output) }
                    item { OutlinedButton(onClick = { call(request(outputKind)) { output = it.text } }, enabled = !busy, modifier = Modifier.fillMaxWidth()) { Text("Refresh ${if (outputKind == "logs") "logs" else "trace"}") } }
                    item { GradientButton("Export full server diagnostics", {
                        call(request("diagnostics_export")) { result -> exportID = result.id; exportTotal = result.total; exportGeneration = session.generation; export.launch("ITOM_server_diagnostics.zip") }
                    }, Modifier.fillMaxWidth(), enabled = !busy, glyph = Glyph.DOWN) }
                    if (busy && exportID.isNotEmpty()) item {
                        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                            Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
                                SmallNote("Saving server diagnostics")
                                SmallNote("${(exportProgress.coerceIn(0f, 1f) * 100).toInt()}%", color = Blue)
                            }
                            ItomProgressBar(exportProgress, modifier = Modifier.fillMaxWidth(), color = Blue)
                        }
                    }
                }
                AdminSection.TRANSPORT -> {
                    item { AdminHeading("Connection resources", "Changes restart the server with automatic rollback protection. Export a new invitation when changing client settings.") }
                    item { InfoCard(Modifier.fillMaxWidth()) { Text("Transport resources", style = MaterialTheme.typography.titleMedium); val t = status.transport; SmallNote("${t.layout} · ${t.data_shards} DATA shards · ${t.account_sessions} account sessions · ${t.endpoint_connections} endpoint sockets"); OutlinedButton(onClick = {
                        form = AdminForm("Transport resources", listOf(AdminField("layout", "Mailbox layout", t.layout, choices = listOf("auto" to "Automatic", "mixed" to "Mixed", "split" to "Split")), AdminField("data_shards", "DATA shards", t.data_shards.toString(), choices = listOf("1" to "1", "2" to "2", "4" to "4")), AdminField("account_sessions", "Sessions per account: 2–128", t.account_sessions.toString()), AdminField("endpoint_connections", "Endpoint sockets: 2–4096", t.endpoint_connections.toString()), AdminField("login_parallelism", "Parallel logins: 1–32", t.login_parallelism.toString()))) { fields ->
                            val numbers = fields.filterKeys { it != "layout" }.mapValues { it.value.toIntOrNull() }
                            if (numbers.values.any { it == null }) onMessage("Enter whole numbers for resource limits.")
                            else change("Update transport resources", "transport") { put("transport", buildJsonObject { put("layout", fields.getValue("layout")); numbers.forEach { (key, value) -> put(key, requireNotNull(value)) } }) }
                        }
                    }, enabled = canChange) { Text("Edit resources") } } }
                    item { InfoCard(Modifier.fillMaxWidth(), accent = Blue) {
                        Text("IMAP routing", style = MaterialTheme.typography.titleMedium)
                        SmallNote("Configure a server IP pin per mailbox or for all accounts from Mail accounts. The mailbox hostname remains the verified TLS identity.")
                        TextButton(onClick = { section = AdminSection.ACCOUNTS }) { Text("Manage mail accounts") }
                    } }
                }
                AdminSection.SERVICE -> {
                    item { AdminHeading("Service control", "Disruptive changes restart the service. A stopped service automatically returns if this admin client cannot reconnect within 60 seconds.") }
                    item { InfoCard(Modifier.fillMaxWidth()) { Text("itom.service", style = MaterialTheme.typography.titleMedium); StatusLabel(status.service.uppercase(), Green); Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState())) { listOf("Start" to "service_start", "Restart" to "service_restart", "Stop" to "service_stop").forEach { (label, operation) -> TextButton(onClick = { change("$label service", operation) }, enabled = canChange) { Text(label, color = if (label == "Stop" && canChange) Red else Blue) } } } } }
                    item { OutlinedButton(onClick = { change("Reinstall current service", "service_install", "Reinstalls the already installed binary and service unit. The previous unit and configuration are retained during the rollback trial.") }, enabled = canChange, modifier = Modifier.fillMaxWidth()) { Text("Reinstall current service") } }
                    item { SmallNote("Permanent mailbox purges, uninstall and journal migrations stay in the CLI. Initial setup and supplying a newer server binary also require local server access.") }
                }
            }
            if (busy && exportID.isEmpty()) item { LinearProgressIndicator(Modifier.fillMaxWidth()) }
        }
    }
    form?.let { dialog ->
        if (dialog.fields.isEmpty()) AlertDialog(onDismissRequest = { form = null; invitation = "" }, title = { Text(dialog.title) }, text = { Column(Modifier.heightIn(max = 400.dp).verticalScroll(rememberScrollState())) { SmallNote("This invitation contains credentials. Share it only with the intended client."); SelectionContainer { Text(invitation, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) } } }, confirmButton = { TextButton(onClick = { saveInvitation.launch("ITOM-invitation.txt") }) { Text("Save invitation") } }, dismissButton = { TextButton(onClick = { form = null; invitation = "" }) { Text("Done") } })
        else AdminFormDialog(dialog, dismiss = { form = null }, submit = { values -> form = null; dialog.submit(values) })
    }
    confirmation?.let { change -> AlertDialog(onDismissRequest = { confirmation = null }, title = { Text(change.title) }, text = { Text(change.detail) }, confirmButton = { TextButton(onClick = { confirmation = null; call(change.request) { onMessage("Change accepted. Keep ITOM connected while it verifies recovery.") } }, enabled = canChange) { Text("Apply with rollback") } }, dismissButton = { TextButton(onClick = { confirmation = null }) { Text("Cancel") } }) }
}

@Composable private fun AdminCategoryRow(first: AdminSection, second: AdminSection, onSelect: (AdminSection) -> Unit) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
        listOf(first, second).forEach { item ->
            InfoCard(Modifier.weight(1f), accent = Blue, onClick = { onSelect(item) }) {
                AppIcon(item.glyph, Modifier.size(24.dp), Blue)
                Text(item.label, style = MaterialTheme.typography.titleMedium, maxLines = 2, overflow = TextOverflow.Ellipsis)
                SmallNote(item.description)
            }
        }
    }
}

@Composable private fun AdminHeading(title: String, subtitle: String) { Column(verticalArrangement = Arrangement.spacedBy(6.dp)) { Text(title, style = MaterialTheme.typography.headlineSmall); SmallNote(subtitle) } }
@Composable private fun AdminStat(label: String, value: String, modifier: Modifier) { InfoCard(modifier) { SmallNote(label); Text(value, style = MaterialTheme.typography.headlineSmall, color = Blue) } }
@Composable private fun AdminOutput(text: String) { InfoCard(Modifier.fillMaxWidth()) { SelectionContainer { Text(text, style = MaterialTheme.typography.bodySmall, fontFamily = FontFamily.Monospace, modifier = Modifier.heightIn(max = 360.dp).verticalScroll(rememberScrollState())) } } }
@Composable private fun AdminTraffic(traffic: JsonObject, users: List<AdminUser>) {
    InfoCard(Modifier.fillMaxWidth()) {
        Text("Live client traffic", style = MaterialTheme.typography.titleMedium)
        var previous by remember { mutableStateOf<JsonObject?>(null) }
        val old=remember(traffic) { previous }
        SideEffect { previous=traffic }
        fun number(obj: JsonObject?,key: String):Long=(obj?.get(key) as? JsonPrimitive)?.longOrNull?:0
        val elapsed=number(traffic,"sampled_at_ms")-number(old,"sampled_at_ms")
        val sameInstance=traffic["instance"]==old?.get("instance") && elapsed>0
        val oldRows=(old?.get("users") as? JsonArray)?.mapNotNull { it as? JsonObject }.orEmpty()
        val rows = traffic["users"] as? JsonArray ?: traffic["Users"] as? JsonArray ?: JsonArray(emptyList())
        if (rows.isEmpty()) SmallNote("Traffic counters appear after the server accepts a client.")
        rows.forEach { item ->
            val row = item as? JsonObject ?: return@forEach
            fun value(vararg keys: String): String = keys.firstNotNullOfOrNull { (row[it] as? JsonPrimitive)?.contentOrNull }.orEmpty()
            val id = value("uid", "ID", "id")
            val up = value("upload_bytes", "Upload", "upload").toLongOrNull() ?: 0
            val down = value("download_bytes", "Download", "download").toLongOrNull() ?: 0
            Text(users.firstOrNull { it.id == id }?.name ?: id, style = MaterialTheme.typography.labelLarge)
            val before=oldRows.firstOrNull { (it["uid"] as? JsonPrimitive)?.contentOrNull==id }
            val upRate=if(sameInstance && before!=null && up>=number(before,"upload_bytes")) "%.1f".format((up-number(before,"upload_bytes"))*8.0/elapsed/1000) else "—"
            val downRate=if(sameInstance && before!=null && down>=number(before,"download_bytes")) "%.1f".format((down-number(before,"download_bytes"))*8.0/elapsed/1000) else "—"
            SmallNote("↑ $upRate · ↓ $downRate Mbps")
            SmallNote("${value("tcp_sockets", "Sockets")} TCP · ↑ ${"%.1f".format(up / 1e6)} MB · ↓ ${"%.1f".format(down / 1e6)} MB")
        }
    }
}
@Composable private fun AdminTrialCard(pending: AdminPending, enabled: Boolean, undo: () -> Unit) {
    var elapsed by remember(pending) { mutableLongStateOf(0) }
    LaunchedEffect(pending) { val start = SystemClock.elapsedRealtime(); while (isActive) { elapsed = SystemClock.elapsedRealtime() - start; delay(1000) } }
    InfoCard(Modifier.fillMaxWidth(), accent = Yellow) {
        Text(if (pending.state == "rolling_back") "Restoring previous configuration" else "Verifying connection", style = MaterialTheme.typography.titleMedium, color = Yellow)
        SmallNote("Keep ITOM connected. The change is kept after 60 seconds of verified two-way connectivity; 60 seconds without recovery triggers rollback.")
        SmallNote("Recovery lease: ${AdminCodec.trialSeconds(pending, elapsed)} s · ${pending.state.replace('_', ' ')}")
        if (pending.state != "rolling_back") TextButton(onClick = undo, enabled = enabled) { Text("Revert now", color = Yellow) }
    }
}
@Composable private fun AdminMessageCard(title: String, original: String, hint: String, enabled: Boolean, fallback: String = "", save: (String) -> Unit) {
    var text by remember(original) { mutableStateOf(original) }
    InfoCard(Modifier.fillMaxWidth(), accent = Blue) {
        Text(title, style = MaterialTheme.typography.titleMedium)
        OutlinedTextField(text, { text = it }, modifier = Modifier.fillMaxWidth(), label = { Text("Message") }, placeholder = { Text(hint) }, minLines = 2, maxLines = 5, isError = !AdminCodec.messageValid(text), enabled = enabled)
        SmallNote("Preview")
        Text(serverMessageText(text.ifEmpty { fallback }).let { if (it.text.isBlank()) androidx.compose.ui.text.AnnotatedString("No server message") else it })
        SmallNote("${text.toByteArray(Charsets.UTF_8).size}/2048 bytes · $hint")
        OutlinedButton(onClick = { save(text) }, enabled = enabled && AdminCodec.messageValid(text)) { Text("Save message") }
    }
}
@Composable private fun AdminFormDialog(form: AdminForm, dismiss: () -> Unit, submit: (Map<String, String>) -> Unit) {
    val values = remember(form) { mutableStateMapOf<String, String>().apply { form.fields.forEach { put(it.key, it.initial) } } }
    AlertDialog(onDismissRequest = dismiss, title = { Text(form.title) }, text = {
        Column(Modifier.heightIn(max = 440.dp).verticalScroll(rememberScrollState()), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            form.fields.forEach { field -> if (field.choices.isNotEmpty()) {
 SmallNote(field.label)
 Row(Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()), horizontalArrangement = Arrangement.spacedBy(6.dp)) { field.choices.forEach { (key, label) -> FilterChip(values[field.key] == key, { values[field.key] = key }, label = { Text(label) }) } }
 } else OutlinedTextField(values[field.key].orEmpty(), { value -> if (value.toByteArray(Charsets.UTF_8).size <= 4096) values[field.key] = value }, label = { Text(field.label) }, modifier = Modifier.fillMaxWidth(), singleLine = !field.multiline, visualTransformation = if (field.secret) PasswordVisualTransformation() else VisualTransformation.None, keyboardOptions = KeyboardOptions(keyboardType = if (field.secret) KeyboardType.Password else KeyboardType.Text)) }
        }
    }, confirmButton = { TextButton(onClick = { submit(values.toMap()) }, enabled = form.fields.all { !it.required || values[it.key].orEmpty().isNotBlank() }) { Text("Continue") } }, dismissButton = { TextButton(onClick = dismiss) { Text("Cancel") } })
}
fun adminError(code: String) = when (code) {
    "ADMIN_ACCESS_DENIED" -> "This server has not granted admin access to this client."
    "ADMIN_CHANGE_PENDING" -> "Wait for the current connection trial to finish, or revert it."
    "ADMIN_WATCHDOG_UNAVAILABLE", "ADMIN_SERVICE_NOT_MANAGED", "ADMIN_SERVICE_NOT_INSTALLED" -> "Changes require the installed Linux service and a working rollback watchdog."
    "ADMIN_CHANGE_INVALID" -> "The server rejected these settings. Check the selected client, account and values."
    "ADMIN_CHANGE_CONFLICT" -> "The server state changed. Refresh before trying again."
    "ADMIN_CONNECTION_FAILED" -> "The admin connection is unavailable. Keep the VPN connected and check the change status before retrying."
    else -> "Server operation failed: $code"
}
