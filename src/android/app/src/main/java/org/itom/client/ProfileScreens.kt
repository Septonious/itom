package org.itom.client

import android.content.Context
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.*
import org.itom.domain.*
import java.io.ByteArrayOutputStream

@OptIn(ExperimentalLayoutApi::class)
@Composable fun ProfilesScreen(config: PublicConfig, busy: Boolean, onSelect: (String) -> Unit, onEdit: (String) -> Unit,
    onDelete: (String) -> Unit, onImport: () -> Unit, onMessage: (String) -> Unit) {
    var expanded by rememberSaveable { mutableStateOf<String?>(null) }
    var removing by remember { mutableStateOf<ServerProfile?>(null) }
    var exportId by rememberSaveable { mutableStateOf<String?>(null) }
    val context = LocalContext.current; val scope = rememberCoroutineScope()
    val exportFile = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/json")) { uri ->
        val profile = config.profiles.find { it.profileId == exportId }; exportId = null
        if (uri != null && profile == null) onMessage("This profile is no longer available for export.")
        if (uri != null && profile != null) scope.launch {
            try {
                withContext(Dispatchers.IO) { context.contentResolver.openOutputStream(uri, "wt")?.use { it.write(Codec.exportPublic(profile).toByteArray()) } ?: throw DomainError("EXPORT_FAILED") }
                onMessage("Metadata exported without passwords or the encryption key.")
            } catch (e: CancellationException) { throw e } catch (_: Exception) { onMessage("Could not export the profile.") }
        }
    }
    Column(Modifier.fillMaxSize().padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text("Server profiles", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
            GlyphButton(Glyph.PLUS, "Add server", onImport, Blue, !busy)
        }
        LazyColumn(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(12.dp), contentPadding = PaddingValues(bottom = 16.dp)) {
            if (config.profiles.isEmpty()) item {
                InfoCard(Modifier.fillMaxWidth()) {
                    IconBadge(Glyph.SERVERS)
                    Text("Add your first server", style = MaterialTheme.typography.titleLarge)
                    SmallNote("Import an invitation containing the profile, accounts and your User ID.")
                    GradientButton("Add profile", onImport, Modifier.fillMaxWidth(), !busy, Glyph.PLUS)
                }
            }
            items(config.profiles, key = { it.profileId }) { profile ->
                val active = profile.profileId == config.selectedProfileId
                InfoCard(Modifier.fillMaxWidth(), accent = if (active) Blue else null) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        IconBadge(Glyph.SERVERS, color = if (active) Blue else Muted)
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                            Text(profile.name, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            SmallNote("${profile.providerId} · ${profile.accounts.count { it.enabled }}/${profile.accounts.size} accounts enabled")
                        }
                        if (active) StatusLabel("Selected", Blue)
                    }
                    if (profile.profileId in config.incompleteProfileIds) Text("Setup required", style = MaterialTheme.typography.labelSmall, color = Yellow)
                    Row(Modifier.fillMaxWidth().padding(vertical = 5.dp), horizontalArrangement = Arrangement.spacedBy(18.dp)) {
                        Column(Modifier.weight(1f)) { SmallNote("User ID"); Text(profile.userId) }
                        Column(Modifier.weight(1f)) { SmallNote("Max IMAP sockets"); Text(profile.totalLines.toString()) }
                    }
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        OutlinedButton(enabled = !busy && !active, onClick = { onSelect(profile.profileId) }, modifier = Modifier.weight(1f), shape = MaterialTheme.shapes.medium) {
                            if (active) { AppIcon(Glyph.CHECK, Modifier.size(16.dp), Blue); Spacer(Modifier.width(6.dp)) }
                            Text(if (active) "Selected" else "Select")
                        }
                        OutlinedButton(enabled = !busy, onClick = { onEdit(profile.profileId) }, modifier = Modifier.weight(1f), shape = MaterialTheme.shapes.medium) { Text("Edit") }
                    }
                    TextButton(modifier = Modifier.fillMaxWidth(), onClick = { expanded = if (expanded == profile.profileId) null else profile.profileId }) { Text(if (expanded == profile.profileId) "Hide details" else "Profile details") }
                    if (expanded == profile.profileId) {
                        HorizontalDivider(color = Outline)
                        SmallNote("Server ID: ${profile.serverId}")
                        SmallNote("Folders: ${profile.mailboxNamespace}")
                        SmallNote(if (profile.profileId in config.incompleteProfileIds) "Enter the key and account credentials in the editor." else "The key is protected by Android Keystore.")
                        profile.accounts.forEach { SmallNote("${it.name} · ${it.host}:${it.port} · ${if (it.enabled) "enabled" else "disabled"}") }
                        SmallNote("Export metadata: servers, folders and IDs, without passwords or keys.")
                        FlowRow(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
                            OutlinedButton(enabled = !busy, onClick = { exportId = profile.profileId; exportFile.launch("ITOM_profile_${profile.profileId}.json") }) {
                                AppIcon(Glyph.DOWN, Modifier.size(16.dp), Blue); Spacer(Modifier.width(6.dp)); Text("Export metadata")
                            }
                            TextButton(enabled = !busy, onClick = { removing = profile }, modifier = Modifier.heightIn(min = 48.dp)) { Text("Remove", color = Red) }
                        }
                    }
                }
            }
        }
    }
    removing?.let { p -> AlertDialog(onDismissRequest = { removing = null }, title = { Text("Remove profile?") }, text = { Text("${p.name}: local settings and secrets will be removed. The server registration will remain.") },
        confirmButton = { TextButton(enabled = !busy, onClick = { onDelete(p.profileId); removing = null }) { Text("Remove", color = Red) } }, dismissButton = { TextButton(onClick = { removing = null }) { Text("Cancel") } }) }
}

internal suspend fun readBounded(context: Context, uri: Uri, limit: Int): ByteArray = withContext(Dispatchers.IO) {
    context.contentResolver.openInputStream(uri)?.use { input ->
        val buffer = ByteArray(8192); val out = ByteArrayOutputStream()
        while (true) { currentCoroutineContext().ensureActive(); val n = input.read(buffer); if (n < 0) break; demand(out.size() + n <= limit, "DOCUMENT_TOO_LARGE"); out.write(buffer, 0, n) }
        out.toByteArray()
    } ?: throw DomainError("FILE_UNAVAILABLE")
}
@Composable fun ImportScreen(busy: Boolean, onMessage: (String) -> Unit, onImport: (Invitation) -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var reading by remember { mutableStateOf(false) }
    Column(Modifier.fillMaxSize().padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        GradientButton("Import from clipboard", {
            // Read only after an explicit tap while this screen is in the foreground.
            // Never retain the raw invitation in Compose state or display its secrets.
            reading = true
            scope.launch {
                try {
                    val clipboard = context.getSystemService(android.content.ClipboardManager::class.java)
                    val clip = clipboard?.primaryClip
                    val raw = if (clip != null && clip.itemCount > 0) clip.getItemAt(0).text?.toString() else null
                    if (raw.isNullOrBlank()) {
                        onMessage("Clipboard does not contain an ITOM invitation.")
                    } else if (raw.length > Codec.INVITATION_LIMIT * 4 / 3 + 32) {
                        onMessage("The invitation is too large.")
                    } else {
                        val invitation = withContext(Dispatchers.Default) { Codec.invitation(raw.trim(), System.currentTimeMillis() / 1000) }
                        onImport(invitation)
                    }
                } catch (e: CancellationException) { throw e }
                catch (e: DomainError) { onMessage(errorText(e.code)) }
                catch (_: Exception) { onMessage("Could not import the invitation.") }
                finally { reading = false }
            }
        }, Modifier.fillMaxWidth(), enabled = !busy && !reading, glyph = Glyph.PLUS)
        if (reading) LinearProgressIndicator(Modifier.fillMaxWidth())
    }
}
