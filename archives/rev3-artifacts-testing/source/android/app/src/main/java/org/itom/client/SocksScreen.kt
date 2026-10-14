package org.itom.client

import android.view.WindowManager
import androidx.activity.compose.LocalActivity
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import androidx.lifecycle.viewmodel.compose.viewModel
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.launch
import org.itom.domain.*

/** Secrets remain in memory only; never Bundle/rememberSaveable/SavedStateHandle. */
class SocksEditor : ViewModel() {
    var draft by mutableStateOf<SocksDraft?>(null); private set
    var busy by mutableStateOf(false); private set
    var error by mutableStateOf<String?>(null); private set
    var saved by mutableStateOf(false); private set
    fun load(vault: VaultStore) {
        if (draft != null || busy) return
        busy = true
        viewModelScope.launch {
            try { draft = vault.socksDraft(); error = null }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { error = "VAULT_UNAVAILABLE" }
            finally { busy = false }
        }
    }
    fun change(value: SocksDraft) { if (!busy) { draft = value; error = null } }
    fun authentication(enabled: Boolean) {
        val d = draft ?: return
        change(d.withAuthentication(enabled))
    }
    fun save(vault: VaultStore) {
        val value = draft ?: return
        if (busy) return
        busy = true
        viewModelScope.launch {
            try { vault.socks(value); draft = null; saved = true }
            catch (e: CancellationException) { throw e }
            catch (e: DomainError) { error = e.code }
            catch (_: Exception) { error = "VAULT_WRITE_FAILED" }
            finally { busy = false }
        }
    }
    fun discard() { if (!busy) { draft = null; error = null; saved = false } }
}

@Composable fun SocksScreen(graph: AppGraph, onSaved: () -> Unit, editor: SocksEditor = viewModel()) {
    var revealPassword by remember { mutableStateOf(false) }
    val activity = LocalActivity.current
    DisposableEffect(activity) {
        val window = activity?.window
        val wasSecure = ((window?.attributes?.flags ?: 0) and WindowManager.LayoutParams.FLAG_SECURE) != 0
        window?.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        onDispose { if (!wasSecure) window?.clearFlags(WindowManager.LayoutParams.FLAG_SECURE); editor.discard() }
    }
    LaunchedEffect(Unit) { editor.load(graph.vault) }
    LaunchedEffect(editor.saved) { if (editor.saved) { editor.discard(); onSaved() } }
    PageColumn {
        SmallNote("Local address: 127.0.0.1. SOCKS runs while VPN is connected. Saving restarts an active connection.")
        val d = editor.draft
        if (editor.busy) LinearProgressIndicator(Modifier.fillMaxWidth())
        if (d != null) {
            OutlinedTextField(d.port, { if (it.length <= 5 && it.all(Char::isDigit)) editor.change(d.copy(port = it)) },
                label = { Text("SOCKS5 port") }, singleLine = true, enabled = !editor.busy,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number), modifier = Modifier.fillMaxWidth())
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text("SOCKS5 authentication", modifier = Modifier.weight(1f))
                ItomSwitch(d.authentication, editor::authentication, enabled = !editor.busy)
            }
            if (d.authentication) {
                OutlinedTextField(d.username, { if (it.toByteArray(Charsets.UTF_8).size <= 255) editor.change(d.copy(username = it)) },
                    label = { Text("Username") }, singleLine = true, enabled = !editor.busy, modifier = Modifier.fillMaxWidth(),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password))
                OutlinedTextField(d.password, { if (it.toByteArray(Charsets.UTF_8).size <= 255) editor.change(d.copy(password = it)) },
                    label = { Text("Password") }, singleLine = true, enabled = !editor.busy, modifier = Modifier.fillMaxWidth(),
                    visualTransformation = if (revealPassword) androidx.compose.ui.text.input.VisualTransformation.None else PasswordVisualTransformation(),
                    trailingIcon = { TextButton(onClick = { revealPassword = !revealPassword }) { Text(if (revealPassword) "Hide" else "Show") } }, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Password))
            }
            SmallNote("Changes apply on the next connection. Credentials are stored in the encrypted app vault.")
            GradientButton("Save", { editor.save(graph.vault) }, enabled = !editor.busy, modifier = Modifier.fillMaxWidth())
        }
        editor.error?.let { SmallNote(errorText(it), Red) }
    }
}
