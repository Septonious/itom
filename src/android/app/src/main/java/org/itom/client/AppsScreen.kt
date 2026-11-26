package org.itom.client

import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.text.KeyboardActions
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import org.itom.domain.*

@Composable fun AppsScreen(saved: AppPolicy, catalogSource: PackageCatalog, busy: Boolean, cancel: () -> Unit, apply: (AppPolicy, List<InstalledApp>) -> Unit) {
    var policy by remember { mutableStateOf(saved) }
    var initialized by remember { mutableStateOf(false) }
    var query by rememberSaveable { mutableStateOf("") }
    var system by rememberSaveable { mutableStateOf(false) }
    var selectedOnly by rememberSaveable { mutableStateOf(false) }
    var catalog by remember { mutableStateOf<List<InstalledApp>>(emptyList()) }
    var loading by remember { mutableStateOf(true) }
    var error by remember { mutableStateOf<String?>(null) }
    var refresh by remember { mutableIntStateOf(0) }
    val keyboard = LocalSoftwareKeyboardController.current
    LaunchedEffect(refresh) {
        loading = true; error = null
        try {
            catalog = catalogSource.load()
            if (!initialized) { policy = AppRouting.migrate(saved, catalog); initialized = true }
        } catch (e: CancellationException) { throw e }
        catch (_: Exception) { error = "Could not load the application list." }
        finally { loading = false }
    }
    val direct = policy.directPackages.toSet()
    val vpn = policy.vpnPackages.toSet()
    val missing = (direct + vpn) - catalog.map { it.packageName }.toSet()
    val groups = remember(catalog) { catalog.groupBy { it.uid } }
    val excluded = remember(policy, catalog) { AppRouting.exclusions(policy, catalog) }
    val visible = remember(catalog, query, system, selectedOnly, direct, vpn) {
        catalog.filter { (system || !it.system) && (!selectedOnly || it.packageName in direct || it.packageName in vpn) &&
            (it.label.contains(query, true) || it.packageName.contains(query, true)) }
    }
    val canApply = !busy && !loading && error == null && missing.isEmpty()
    Column(Modifier.fillMaxSize().padding(horizontal = 20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(Modifier.fillMaxWidth().heightIn(min = 60.dp), verticalAlignment = Alignment.CenterVertically) {
            GlyphButton(Glyph.BACK, "Cancel selection and return to rules", cancel, Blue, !busy)
            Column(Modifier.weight(1f)) {
                Text("Applications", style = MaterialTheme.typography.headlineSmall)
                Text("Direct: ${direct.size} · VPN: ${vpn.size}", color = Muted, style = MaterialTheme.typography.bodySmall)
            }
            GlyphButton(Glyph.REFRESH, "Refresh applications", { refresh++ }, enabled = !loading && !busy)
        }
        OutlinedTextField(query, { if (it.length <= 256) query = it }, modifier = Modifier.fillMaxWidth(), singleLine = true,
            placeholder = { Text("Search applications") }, leadingIcon = { AppIcon(Glyph.SEARCH, color = Blue) },
            trailingIcon = { if (query.isNotEmpty()) GlyphButton(Glyph.CLOSE, "Clear search", { query = "" }) },
            shape = MaterialTheme.shapes.large, keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { keyboard?.hide() }))
        LazyColumn(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(9.dp), contentPadding = PaddingValues(bottom = 8.dp)) {
            item(key = "options") {
                InfoCard(Modifier.fillMaxWidth(), contentPadding = 12.dp) {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Text("Russian apps direct automatically", Modifier.weight(1f))
                        Switch(checked = policy.autoRussian, enabled = !busy, onCheckedChange = { policy = policy.copy(autoRussian = it) })
                    }
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Text("System applications", Modifier.weight(1f))
                        ItomSwitch(checked = system, onCheckedChange = { system = it })
                    }
                    SmallNote("Direct apps bypass VPN and all address rules. VPN apps still follow destination rules.")
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        FilterChip(selected = !selectedOnly, onClick = { selectedOnly = false }, label = { Text("All") })
                        FilterChip(selected = selectedOnly, onClick = { selectedOnly = true }, label = { Text("Overrides") })
                        Spacer(Modifier.weight(1f))
                        Text("${visible.size}", Modifier.align(Alignment.CenterVertically), color = Muted)
                    }
                }
            }
            if (!loading && error == null && missing.isNotEmpty()) item(key = "missing") {
                InfoCard(Modifier.fillMaxWidth(), accent = Red) {
                    SmallNote("Missing applications: ${missing.size}", color = Red)
                    TextButton(onClick = {
                        policy = policy.copy(directPackages = (direct - missing).sorted(), vpnPackages = (vpn - missing).sorted())
                    }, enabled = !busy) { Text("Remove missing applications") }
                }
            }
            if (loading) item(key = "loading") { LinearProgressIndicator(Modifier.fillMaxWidth()) }
            if (error != null) item(key = "error") {
                InfoCard(Modifier.fillMaxWidth(), accent = Red) {
                    SmallNote(error!!, color = Red)
                    TextButton(onClick = { refresh++ }) { Text("Retry") }
                }
            }
            items(visible, key = { "app:${it.packageName}" }, contentType = { "app" }) { app ->
                val choice = AppRouting.choice(policy, app.packageName)
                val group = groups[app.uid].orEmpty()
                val pinned = AppRouting.groupCaptured(group)
                InfoCard(Modifier.fillMaxWidth(), contentPadding = 12.dp) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                        InstalledAppIcon(app.packageName, catalogSource, refresh)
                        Column(Modifier.weight(1f)) {
                            Text(app.label, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(app.packageName, color = Muted, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(if (app.packageName in excluded) "Effective: direct" else "Effective: VPN", style = MaterialTheme.typography.labelSmall, color = Blue)
                            if (group.size > 1) Text("Shared UID · ${group.size}", color = Muted, style = MaterialTheme.typography.labelSmall)
                        }
                    }
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(7.dp)) {
                        listOf(AppRouteChoice.AUTO to "Auto", AppRouteChoice.DIRECT to "Direct", AppRouteChoice.VPN to "VPN").forEach { (option, title) ->
                            FilterChip(selected = choice == option, label = { Text(title) }, enabled = !busy && (!pinned || option != AppRouteChoice.DIRECT),
                                onClick = { policy = AppRouting.select(policy, app, catalog, option) })
                        }
                    }
                }
            }
        }
        Row(Modifier.fillMaxWidth().padding(bottom = 10.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            OutlinedButton(onClick = cancel, enabled = !busy, modifier = Modifier.weight(1f).heightIn(min = 48.dp), shape = MaterialTheme.shapes.medium) { Text("Cancel") }
            GradientButton(if (busy) "Saving…" else "Save", {
                keyboard?.hide()
                apply(policy, catalog)
            }, Modifier.weight(1.3f), enabled = canApply, glyph = Glyph.CHECK)
        }
    }
}

@Composable private fun InstalledAppIcon(packageName: String, source: PackageCatalog, refresh: Int) {
    val bitmap by produceState<ImageBitmap?>(null, packageName, refresh) { value = source.icon(packageName) }
    val icon = bitmap
    if (icon != null) Image(icon, contentDescription = null, modifier = Modifier.size(42.dp))
    else IconBadge(Glyph.APPS, modifier = Modifier.size(42.dp))
}
