package org.itom.client

import android.content.Context
import android.graphics.ImageDecoder
import android.net.Uri
import android.view.WindowManager
import androidx.activity.compose.LocalActivity
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.google.zxing.BinaryBitmap
import com.google.zxing.DecodeHintType
import com.google.zxing.MultiFormatReader
import com.google.zxing.RGBLuminanceSource
import com.google.zxing.common.HybridBinarizer
import kotlinx.coroutines.*
import org.itom.domain.*
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import java.security.MessageDigest
import java.util.Base64

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
                        Column(Modifier.weight(1f)) { SmallNote("Active lanes"); Text(profile.totalLines.toString()) }
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

private suspend fun readBounded(context: Context, uri: Uri, limit: Int): ByteArray = withContext(Dispatchers.IO) {
    context.contentResolver.openInputStream(uri)?.use { input ->
        val buffer = ByteArray(8192); val out = ByteArrayOutputStream()
        while (true) { currentCoroutineContext().ensureActive(); val n = input.read(buffer); if (n < 0) break; demand(out.size() + n <= limit, "DOCUMENT_TOO_LARGE"); out.write(buffer, 0, n) }
        out.toByteArray()
    } ?: throw DomainError("FILE_UNAVAILABLE")
}
private suspend fun decodeQr(context: Context, uri: Uri): String = withContext(Dispatchers.IO) {
    val bytes = readBounded(context, uri, 8 * 1024 * 1024)
    try {
        val bitmap = ImageDecoder.decodeBitmap(ImageDecoder.createSource(ByteBuffer.wrap(bytes))) { decoder, info, _ ->
            demand(info.size.width > 0 && info.size.height > 0, "QR_NOT_FOUND")
            val scale = maxOf(info.size.width, info.size.height) / 2048.0
            if (scale > 1) decoder.setTargetSize((info.size.width / scale).toInt().coerceAtLeast(1), (info.size.height / scale).toInt().coerceAtLeast(1))
            decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
        }
        try {
            val pixels = IntArray(bitmap.width * bitmap.height)
            try {
                bitmap.getPixels(pixels, 0, bitmap.width, 0, 0, bitmap.width, bitmap.height)
                val source = RGBLuminanceSource(bitmap.width, bitmap.height, pixels)
                MultiFormatReader().decode(BinaryBitmap(HybridBinarizer(source)), mapOf(DecodeHintType.TRY_HARDER to true)).text
            } finally { pixels.fill(0) }
        } finally { bitmap.recycle() }
    } catch (e: CancellationException) { throw e }
    catch (e: DomainError) { throw e } catch (_: Exception) { throw DomainError("QR_NOT_FOUND") }
    finally { bytes.fill(0) }
}

@Composable fun ImportScreen(busy: Boolean, onMessage: (String) -> Unit, onImport: (Invitation) -> Unit) {
    // Secrets are never rememberSaveable, copied to logs, or placed on the clipboard by this app.
    var text by remember { mutableStateOf("") }
    var parsed by remember { mutableStateOf<Invitation?>(null) }
    var lintError by remember { mutableStateOf<String?>(null) }
    var reading by remember { mutableStateOf(false) }
    val context = LocalContext.current; val scope = rememberCoroutineScope()
    val activity = LocalActivity.current
    DisposableEffect(activity) {
        val window = activity?.window
        val hadSecure = ((window?.attributes?.flags ?: 0) and WindowManager.LayoutParams.FLAG_SECURE) != 0
        window?.addFlags(WindowManager.LayoutParams.FLAG_SECURE)
        onDispose { if (!hadSecure) window?.clearFlags(WindowManager.LayoutParams.FLAG_SECURE) }
    }
    fun lintText(issue: Codec.ProfileLint): String = errorText(issue.code) +
        if (issue.line > 0) " Line ${issue.line}, column ${issue.column}." else ""
    LaunchedEffect(text, reading) {
        if (text.isBlank() || reading) return@LaunchedEffect
        delay(250)
        val raw = text
        val now = System.currentTimeMillis() / 1000
        val issue = withContext(Dispatchers.Default) { Codec.lintInvitation(raw, now) }
        lintError = issue?.let(::lintText)
        parsed = if (issue == null) withContext(Dispatchers.Default) { Codec.invitation(raw, now) } else null
    }
    fun read(uri: Uri?, image: Boolean) {
        if (uri == null) return
        reading = true; parsed = null; lintError = null
        scope.launch {
            try {
                text = if (image) decodeQr(context, uri) else {
                    val bytes = readBounded(context, uri, Codec.INVITATION_LIMIT * 4 / 3 + 32)
                    try { Charsets.UTF_8.newDecoder().decode(ByteBuffer.wrap(bytes)).toString() } finally { bytes.fill(0) }
                }
                text = withContext(Dispatchers.Default) { Codec.formatInvitation(text) }
                parsed = withContext(Dispatchers.Default) { Codec.invitation(text, System.currentTimeMillis() / 1000) }
            } catch (e: CancellationException) { throw e }
            catch (e: DomainError) { lintError = withContext(Dispatchers.Default) { Codec.lintInvitation(text, System.currentTimeMillis() / 1000)?.let(::lintText) } ?: errorText(e.code) }
            catch (_: Exception) { lintError = "Could not read the invitation." }
            finally { reading = false }
        }
    }
    val document = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { read(it, false) }
    val image = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { read(it, true) }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
        SmallNote("Use an invitation received from the server administrator through a trusted channel. It contains the key and passwords.")
        Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            OutlinedButton(enabled = !busy && !reading, onClick = { document.launch(arrayOf("application/json", "text/plain", "application/octet-stream")) }) { Text("JSON file") }
            OutlinedButton(enabled = !busy && !reading, onClick = { image.launch(arrayOf("image/*")) }) { Text("QR from image") }
        }
        OutlinedTextField(value = text, onValueChange = { if (it.toByteArray(Charsets.UTF_8).size <= Codec.INVITATION_LIMIT * 4 / 3 + 32) { text = it; parsed = null; lintError = null } },
            modifier = Modifier.fillMaxWidth(), shape = MaterialTheme.shapes.medium, label = { Text("ITOM link or JSON profile") }, minLines = 6, maxLines = 16,
            textStyle = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
            visualTransformation = remember { ProfileJsonHighlight() },
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Text, autoCorrectEnabled = false, imeAction = ImeAction.Default),
            isError = lintError != null, enabled = !reading && !busy)
        TextButton(enabled = text.isNotBlank() && !reading && !busy, onClick = {
            reading = true; parsed = null; lintError = null
            scope.launch {
                try {
                    val formatted = withContext(Dispatchers.Default) { Codec.formatInvitation(text) }
                    val invitation = withContext(Dispatchers.Default) { Codec.invitation(formatted, System.currentTimeMillis() / 1000) }
                    text = formatted; parsed = invitation
                }
                catch (e: CancellationException) { throw e }
                catch (e: DomainError) { lintError = withContext(Dispatchers.Default) { Codec.lintInvitation(text, System.currentTimeMillis() / 1000)?.let(::lintText) } ?: errorText(e.code) }
                finally { reading = false }
            }
        }) { Text("Validate and format") }
        lintError?.let { SmallNote(it, color = Red) }
        if (reading) LinearProgressIndicator(Modifier.fillMaxWidth())
        parsed?.let { invitation ->
            val profile = invitation.profile
            val fingerprint = remember(invitation) {
                val raw = Base64.getDecoder().decode(invitation.pskBase64)
                try { MessageDigest.getInstance("SHA-256").digest(raw).joinToString("") { "%02x".format(it.toInt() and 255) }.chunked(8).joinToString(" ") }
                finally { raw.fill(0) }
            }
            InfoCard(Modifier.fillMaxWidth()) {
                Text(profile.name, style = MaterialTheme.typography.titleLarge)
                Text("User ID: ${profile.userId}")
                SmallNote("Server ID: ${profile.serverId}")
                SmallNote("${profile.providerId} · ${profile.accounts.count { it.enabled }}/${profile.accounts.size} accounts enabled · ${profile.totalLines} lanes")
                profile.accounts.map { "${it.host}:${it.port}" }.distinct().forEach { SmallNote(it) }
                SmallNote("Folders: ${profile.mailboxNamespace}")
                SmallNote("Key SHA-256: $fingerprint")
            }
            GradientButton("Add profile", { onImport(invitation) }, Modifier.fillMaxWidth(), enabled = !busy && !reading, glyph = Glyph.PLUS)
        }
        Spacer(Modifier.height(16.dp))
    }
}
