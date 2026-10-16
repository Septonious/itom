package org.itom.client

import android.view.WindowManager
import androidx.activity.compose.BackHandler
import androidx.activity.compose.LocalActivity
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.text.input.VisualTransformation
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import org.itom.domain.*

data class ProfileEditorState(
    val profileId: String? = null, val session: ProfileEditSession? = null, val draft: ProfileDraft? = null,
    val loading: Boolean = false, val saving: Boolean = false, val error: String? = null,
    val saved: Boolean = false, val restartFailed: Boolean = false,
) { override fun toString() = "ProfileEditorState(<redacted>)" }

/** Survives rotation in memory, with no SavedStateHandle, Bundle or persistent plaintext draft. */
class ProfileEditorViewModel : ViewModel() {
    private val mutable = MutableStateFlow(ProfileEditorState())
    val state = mutable.asStateFlow()
    private var job: Job? = null

    fun open(vault: VaultStore, id: String, reload: Boolean = false) {
        if (mutable.value.saving || (!reload && mutable.value.profileId == id)) return
        discardDraft()
        mutable.value = ProfileEditorState(profileId = id, loading = true)
        job = viewModelScope.launch {
            try {
                val session = vault.editSession(id)
                mutable.value = ProfileEditorState(profileId = id, session = session, draft = session.initial)
            } catch (e: CancellationException) { throw e }
            catch (e: DomainError) { mutable.value = ProfileEditorState(profileId = id, error = e.code) }
            catch (_: Exception) { mutable.value = ProfileEditorState(profileId = id, error = "VAULT_UNAVAILABLE") }
        }
    }

    fun change(transform: (ProfileDraft) -> ProfileDraft) {
        val current = mutable.value
        val draft = current.draft ?: return
        if (!current.saving) mutable.value = current.copy(draft = transform(draft), error = null)
    }

    fun save(graph: AppGraph) {
        val current = mutable.value
        val session = current.session ?: return
        val draft = current.draft ?: return
        if (current.saving) return
        mutable.value = current.copy(saving = true, error = null)
        job = viewModelScope.launch {
            try { graph.vault.editProfile(session.profileId, session.revision, draft) }
            catch (e: CancellationException) { throw e }
            catch (e: DomainError) { mutable.value = current.copy(error = e.code); return@launch }
            catch (_: Exception) { mutable.value = current.copy(error = "VAULT_WRITE_FAILED"); return@launch }

            // Inspect the live session after the commit. Stop during saving must remain stopped.
            var restartFailed = false
            val active = graph.backend.snapshot.value
            val config = graph.vault.state.value
            if (active.profileId == session.profileId && active.phase in listOf(Phase.CONNECTING, Phase.CONNECTED) && active.configRevision < config.revision) {
                config.profiles.find { it.profileId == session.profileId }?.let { profile ->
                    try { graph.backend.start(profile, config.revision) }
                    catch (_: Exception) { restartFailed = true; runCatching { graph.backend.stop() } }
                }
            }
            // A restart problem must never report an already committed edit as an unsaved draft.
            mutable.value = ProfileEditorState(profileId = session.profileId, saved = true, restartFailed = restartFailed)
        }
    }

    fun discardDraft() { job?.cancel(); job = null; mutable.value = ProfileEditorState() }
    override fun onCleared() { discardDraft() }
}

@Composable fun ProfileEditorScreen(profileId: String, graph: AppGraph, close: () -> Unit, saved: (Boolean) -> Unit,
    editor: ProfileEditorViewModel = viewModel()) {
    val state by editor.state.collectAsStateWithLifecycle()
    var discard by remember { mutableStateOf(false) }
    var reload by remember { mutableStateOf(false) }
    var expanded by rememberSaveable(profileId) { mutableStateOf<String?>(null) }
    val activity = LocalActivity.current
    DisposableEffect(activity) {
        val window = activity?.window
        val hadSecure = ((window?.attributes?.flags ?: 0) and WindowManager.LayoutParams.FLAG_SECURE) != 0
        window?.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        onDispose { if (!hadSecure) window?.clearFlags(WindowManager.LayoutParams.FLAG_SECURE) }
    }
    LaunchedEffect(profileId) { editor.open(graph.vault, profileId) }
    LaunchedEffect(state.saved) {
        if (state.saved) { val restartFailed = state.restartFailed; editor.discardDraft(); saved(restartFailed) }
    }
    fun exit() { editor.discardDraft(); close() }
    fun requestExit() {
        if (state.saving) return
        if (state.draft != null && state.draft != state.session?.initial) discard = true else exit()
    }
    // Own both system Back and the screen header; neither can abandon an in-flight commit.
    BackHandler { requestExit() }
    val draft = state.draft
    val session = state.session
    Column(Modifier.fillMaxSize().padding(horizontal = 20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(Modifier.fillMaxWidth().heightIn(min = 64.dp), verticalAlignment = Alignment.CenterVertically) {
            GlyphButton(Glyph.BACK, "Back to profiles", ::requestExit, Blue, !state.saving)
            Column(Modifier.weight(1f)) {
                Text("Profile editor", style = MaterialTheme.typography.titleLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                SmallNote("Server and mail accounts")
            }
        }
        if (state.loading) LinearProgressIndicator(Modifier.fillMaxWidth())
        if (draft == null || session == null) {
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(12.dp)) {
                state.error?.let { code ->
                    SmallNote(errorText(code), color = Red)
                    GradientButton("Retry", { editor.open(graph.vault, profileId, reload = true) })
                }
            }
        } else {
            LazyColumn(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(12.dp), contentPadding = PaddingValues(bottom = 8.dp)) {
                item(key = "general") {
                    InfoCard(Modifier.fillMaxWidth()) {
                        Text("Profile", style = MaterialTheme.typography.titleMedium)
                        ProfileField("Name", draft.name, 80, !state.saving) { value -> editor.change { it.copy(name = value) } }
                        ProfileField("User ID · 8 digits", draft.userId, 8, !state.saving, KeyboardType.Number) { value -> editor.change { it.copy(userId = value) } }
                        ProfileField("Mail provider", draft.providerId, 64, !state.saving, help = "Use one provider for all accounts, for example rambler or mailru.") { value -> editor.change { it.copy(providerId = value) } }
                        SmallNote("Connection details must match the server settings.")
                    }
                }
                item(key = "server") {
                    InfoCard(Modifier.fillMaxWidth()) {
                        Text("Server settings", style = MaterialTheme.typography.titleMedium)
                        ProfileField("Server ID", draft.serverId, 32, !state.saving, help = "32 hexadecimal characters") { value -> editor.change { it.copy(serverId = value) } }
                        ProfileField("Mailbox namespace", draft.mailboxNamespace, 128, !state.saving) { value -> editor.change { it.copy(mailboxNamespace = value) } }
                        ProfileField("Pair ID", draft.pairId, 32, !state.saving, help = "32 hexadecimal characters") { value -> editor.change { it.copy(pairId = value) } }
                        SecretProfileField("Encryption key · Base64", draft.pskBase64, 128, !state.saving) { value -> editor.change { it.copy(pskBase64 = value) } }
                        SmallNote("Key: 32 bytes in Base64. Passwords and keys are encrypted at rest.")
                    }
                }
                item(key = "account-header") {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Text("IMAP accounts · ${draft.accounts.size}", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                        GlyphButton(Glyph.PLUS, "Add IMAP account", {
                            val account = ProfileEditing.newAccount(draft.accounts)
                            editor.change { it.copy(accounts = it.accounts + account) }; expanded = account.localRef
                        }, Blue, !state.saving)
                    }
                    SmallNote("IMAPS with certificate verification. Keep at least one account enabled.")
                }
                items(draft.accounts, key = { it.localRef }) { account ->
                    val open = expanded == account.localRef
                    InfoCard(Modifier.fillMaxWidth(), accent = if (open) Blue else null) {
                        Row(Modifier.fillMaxWidth().clickable(enabled = !state.saving, role = Role.Button,
                            onClickLabel = if (open) "Collapse account" else "Edit account",
                            onClick = { expanded = if (open) null else account.localRef }),
                            verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                            AppIcon(Glyph.MAIL, color = if (account.enabled) Blue else Muted)
                            Column(Modifier.weight(1f)) {
                                Text(account.name.ifBlank { "New account" }, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                SmallNote(if (account.enabled) "${account.host}:${account.port}" else "Disabled")
                            }
                            AppIcon(if (open) Glyph.DOWN else Glyph.NEXT, color = Blue)
                        }
                        if (open) {
                            fun change(transform: (MailAccountDraft) -> MailAccountDraft) {
                                editor.change { p -> p.copy(accounts = p.accounts.map { if (it.localRef == account.localRef) transform(it) else it }) }
                            }
                            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                                Text("Enable account", Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
                                ItomSwitch(account.enabled, { enabled -> change { it.copy(enabled = enabled) } }, enabled = !state.saving)
                            }
                            ProfileField("Account name", account.name, 320, !state.saving) { value -> change { it.copy(name = value) } }
                            ProfileField("IMAP server", account.host, 253, !state.saving, KeyboardType.Uri) { value -> change { it.copy(host = value) } }
                            ProfileField("Pinned IMAP IP (optional)", account.dialIP, 45, !state.saving, KeyboardType.Uri) { value -> change { it.copy(dialIP = value) } }
                            SmallNote("Blank uses DNS. The IMAP server name is still verified by TLS.")
                            ProfileField("Port", account.port, 5, !state.saving, KeyboardType.Number) { value -> change { it.copy(port = value) } }
                            ProfileField("Email", account.username, 512, !state.saving, KeyboardType.Email) { value -> change { it.copy(username = value) } }
                            SecretProfileField("Password", account.password, 4096, !state.saving) { value -> change { it.copy(password = value) } }
                            TextButton(enabled = !state.saving, onClick = {
                                editor.change { it.copy(accounts = it.accounts.filterNot { a -> a.localRef == account.localRef }) }; expanded = null
                            }) { Text("Remove account", color = Red) }
                        }
                    }
                }
                if (draft.accounts.isEmpty()) item(key = "empty") { SmallNote("Add at least one IMAP account.", color = Yellow) }
            }
            state.error?.let { code ->
                SmallNote(errorText(code), color = Red)
                if (code == "PROFILE_EDIT_CONFLICT") TextButton(onClick = { reload = true }) { Text("Reload latest data") }
            }
            Row(Modifier.fillMaxWidth().padding(bottom = 8.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                OutlinedButton(onClick = ::requestExit, enabled = !state.saving, modifier = Modifier.weight(1f).heightIn(min = 48.dp)) { Text("Cancel") }
                GradientButton(if (state.saving) "Saving…" else "Save", { editor.save(graph) }, Modifier.weight(1f),
                    enabled = !state.saving && draft != session.initial, glyph = Glyph.CHECK)
            }
        }
    }
    if (discard || reload) AlertDialog(onDismissRequest = { discard = false; reload = false }, title = { Text("Discard changes?") },
        text = { Text(if (reload) "The draft will be replaced with the latest saved data." else "This profile has unsaved changes.") },
        confirmButton = { TextButton(onClick = {
            if (reload) { reload = false; editor.open(graph.vault, profileId, reload = true) } else { discard = false; exit() }
        }) { Text(if (reload) "Load" else "Leave") } },
        dismissButton = { TextButton(onClick = { discard = false; reload = false }) { Text("Continue") } })
}

@Composable private fun ProfileField(label: String, value: String, limit: Int, enabled: Boolean,
    keyboard: KeyboardType = KeyboardType.Text, help: String? = null, change: (String) -> Unit) {
    OutlinedTextField(value, { if (it.length <= limit) change(it) }, modifier = Modifier.fillMaxWidth(), enabled = enabled,
        label = { Text(label) }, singleLine = true, shape = MaterialTheme.shapes.medium,
        keyboardOptions = KeyboardOptions(keyboardType = keyboard, imeAction = ImeAction.Next),
        supportingText = if (help == null) null else ({ SmallNote(help) }))
}

@Composable private fun SecretProfileField(label: String, value: String, limit: Int, enabled: Boolean, change: (String) -> Unit) {
    var visible by remember { mutableStateOf(false) }
    OutlinedTextField(value, { if (it.length <= limit) change(it) }, modifier = Modifier.fillMaxWidth(), enabled = enabled,
        label = { Text(label) }, singleLine = true, shape = MaterialTheme.shapes.medium,
        visualTransformation = if (visible) VisualTransformation.None else PasswordVisualTransformation(),
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password, imeAction = ImeAction.Next),
        trailingIcon = { TextButton(onClick = { visible = !visible }, enabled = enabled) { Text(if (visible) "Hide" else "Show", color = Ink) } })
}
