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
@Composable fun ProfilesScreen(config: PublicConfig, session: SessionSnapshot, busy: Boolean, onSelect: (String) -> Unit, onEdit: (String) -> Unit,
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
    Column(Modifier.fillMaxSize().padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        Text("Server profiles", style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.fillMaxWidth())
        LazyColumn(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(8.dp), contentPadding = PaddingValues(bottom = 16.dp)) {
            if (config.profiles.isEmpty()) item {
                InfoCard(Modifier.fillMaxWidth()) {
                    IconBadge(Glyph.SERVERS)
                    Text("No server profiles", style = MaterialTheme.typography.titleLarge)
                    SmallNote("Copy your invitation, then tap Import from clipboard.")
                }
            }
            items(config.profiles, key = { it.profileId }) { profile ->
                val active = profile.profileId == config.selectedProfileId
                InfoCard(Modifier.fillMaxWidth(), accent = if (active) Blue else null, contentPadding = 12.dp,
                    onClick = if (!busy && !active) ({ onSelect(profile.profileId) }) else null) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        IconBadge(Glyph.SERVERS, color = if (active) Blue else Muted)
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(0.dp)) {
                            ExitIdentityTitle(session.exitIdentities[profile.profileId])
                            SmallNote("${profile.accounts.count { it.enabled }} accounts")
                        }
                        if (active) StatusLabel("Selected", Blue)
                    }
                    if (profile.profileId in config.incompleteProfileIds) Text("Setup required", style = MaterialTheme.typography.labelSmall, color = Yellow)
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                        OutlinedButton(enabled = !busy, onClick = { onEdit(profile.profileId) }, modifier = Modifier.weight(1f), shape = MaterialTheme.shapes.medium) { Text("Edit") }
                        TextButton(modifier = Modifier.weight(1f), onClick = { expanded = if (expanded == profile.profileId) null else profile.profileId }) {
                            Text(if (expanded == profile.profileId) "Hide details" else "Profile details")
                        }
                    }
                    if (expanded == profile.profileId) {
                        HorizontalDivider(color = Outline)
                        SmallNote("Server ID: ${profile.serverId}")
                        SmallNote("User ID: ${profile.userId}")
                        SmallNote("Provider: ${profile.providerId}")
                        SmallNote("Max IMAP sockets: ${profile.totalLines}")
                        SmallNote("Folders: ${profile.mailboxNamespace}")
                        SmallNote(if (profile.profileId in config.incompleteProfileIds) "Enter the key and account credentials in the editor." else "The key is protected by Android Keystore.")
                        profile.accounts.forEachIndexed { index, account -> SmallNote("Account ${index + 1} · ${account.host}:${account.port} · ${if (account.enabled) "enabled" else "disabled"}") }
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
            item(key = "import-from-clipboard") {
                GradientButton("Import from clipboard", onImport, Modifier.fillMaxWidth(), !busy, Glyph.PLUS)
            }
        }
    }
    removing?.let { p -> AlertDialog(onDismissRequest = { removing = null }, title = { Text("Remove profile?") }, text = { Text("Local settings and secrets for this profile will be removed. The server registration will remain.") },
        confirmButton = { TextButton(enabled = !busy, onClick = { onDelete(p.profileId); removing = null }) { Text("Remove", color = Red) } }, dismissButton = { TextButton(onClick = { removing = null }) { Text("Cancel") } }) }
}

internal suspend fun readBounded(context: Context, uri: Uri, limit: Int): ByteArray = withContext(Dispatchers.IO) {
    context.contentResolver.openInputStream(uri)?.use { input ->
        val buffer = ByteArray(8192); val out = ByteArrayOutputStream()
        while (true) { currentCoroutineContext().ensureActive(); val n = input.read(buffer); if (n < 0) break; demand(out.size() + n <= limit, "DOCUMENT_TOO_LARGE"); out.write(buffer, 0, n) }
        out.toByteArray()
    } ?: throw DomainError("FILE_UNAVAILABLE")
}
