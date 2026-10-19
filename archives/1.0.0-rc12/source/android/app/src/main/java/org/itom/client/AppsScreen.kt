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
    var mode by rememberSaveable { mutableStateOf(saved.mode) }
    var selected by rememberSaveable { mutableStateOf(saved.packages.toList()) }
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
        try { catalog = catalogSource.load() } catch (e: CancellationException) { throw e } catch (_: Exception) { error = "Could not load the application list." }
        finally { loading = false }
    }
    val selectedSet = remember(selected) { selected.toSet() }
    val visible = remember(catalog, query, system, selectedOnly, selectedSet, mode) {
        catalog.filter { (system || !it.system) && (!selectedOnly || mode == AppMode.ALL || it.packageName in selectedSet) &&
            (it.label.contains(query, true) || it.packageName.contains(query, true)) }
    }
    val missing = remember(selectedSet, catalog, mode) { if (mode == AppMode.ALL) emptySet() else selectedSet - catalog.map { it.packageName }.toSet() }
    val groupSizes = remember(catalog) { catalog.groupingBy { it.uid }.eachCount() }
    val canApply = !busy && !loading && error == null && missing.isEmpty() && (mode != AppMode.INCLUDE_SELECTED || selected.isNotEmpty())
    Column(Modifier.fillMaxSize().padding(horizontal = 20.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Row(Modifier.fillMaxWidth().heightIn(min = 60.dp), verticalAlignment = Alignment.CenterVertically) {
            GlyphButton(Glyph.BACK, "Cancel selection and return to rules", cancel, Blue, !busy)
            Column(Modifier.weight(1f)) {
                Text("Applications", style = MaterialTheme.typography.headlineSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(if (mode == AppMode.ALL) "All applications use VPN" else "Selected: ${selected.size}", color = Muted, style = MaterialTheme.typography.bodySmall)
            }
            GlyphButton(Glyph.REFRESH, "Refresh applications", { refresh++ }, enabled = !loading && !busy)
        }
        OutlinedTextField(query, { if (it.length <= 256) query = it }, modifier = Modifier.fillMaxWidth(), singleLine = true,
            placeholder = { Text("Search applications") }, leadingIcon = { AppIcon(Glyph.SEARCH, color = Blue) },
            trailingIcon = { if (query.isNotEmpty()) GlyphButton(Glyph.CLOSE, "Clear search", { query = "" }) },
            shape = MaterialTheme.shapes.large, keyboardOptions = KeyboardOptions(imeAction = ImeAction.Search),
            keyboardActions = KeyboardActions(onSearch = { keyboard?.hide() }))
        // Search and actions are pinned; options and applications share the full remaining viewport.
        LazyColumn(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(9.dp), contentPadding = PaddingValues(bottom = 8.dp)) {
            item(key = "options") {
                Column(verticalArrangement = Arrangement.spacedBy(9.dp)) {
                    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(7.dp)) {
                        listOf(AppMode.ALL to "All through VPN", AppMode.EXCLUDE_SELECTED to "Direct", AppMode.INCLUDE_SELECTED to "VPN only").forEach { (value, label) ->
                            val active = mode == value
                            InfoCard(Modifier.weight(1f).semantics { this.selected = active }, accent = if (active) Blue else null,
                                contentPadding = 9.dp, onClick = if (!busy) ({ mode = value }) else null) {
                                Column(Modifier.fillMaxWidth().heightIn(min = 44.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                                    Text(label, style = MaterialTheme.typography.labelSmall, color = if (active) Blue else Muted, maxLines = 2)
                                }
                            }
                        }
                    }
                    SmallNote(when (mode) {
                        AppMode.ALL -> "Address rules apply to all applications."
                        AppMode.EXCLUDE_SELECTED -> "Selected applications bypass VPN and all address rules, including Block."
                        AppMode.INCLUDE_SELECTED -> "Only selected applications use VPN. Others bypass it, including Block rules."
                    })
                    InfoCard(Modifier.fillMaxWidth(), contentPadding = 12.dp) {
                        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                            AppIcon(Glyph.SETTINGS, color = Blue)
                            Text("System applications", Modifier.weight(1f).padding(horizontal = 10.dp), style = MaterialTheme.typography.bodyMedium)
                            ItomSwitch(checked = system, onCheckedChange = { system = it })
                        }
                    }
                    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                        FilterChip(selected = !selectedOnly, onClick = { selectedOnly = false }, label = { Text("All") })
                        FilterChip(selected = selectedOnly, onClick = { selectedOnly = true }, label = { Text("Selected") })
                        Spacer(Modifier.weight(1f))
                        Text("${visible.size}", Modifier.align(Alignment.CenterVertically), color = Muted, style = MaterialTheme.typography.labelMedium)
                    }
                    if (mode != AppMode.ALL && selected.isNotEmpty()) SmallNote("Applications sharing an Android UID are selected together.")
                }
            }
            if (!loading && error == null && missing.isNotEmpty()) item(key = "missing") {
                InfoCard(Modifier.fillMaxWidth(), accent = Red, contentPadding = 12.dp) {
                    SmallNote("Selected applications no longer installed: ${missing.size}", color = Red)
                    TextButton(enabled = !busy, onClick = { selected = (selectedSet - missing).toList() }) { Text("Remove missing applications") }
                }
            }
            if (loading) item(key = "loading") { LinearProgressIndicator(Modifier.fillMaxWidth()) }
            if (error != null) item(key = "error") {
                InfoCard(Modifier.fillMaxWidth(), accent = Red) {
                    SmallNote(error!!, color = Red)
                    TextButton(onClick = { refresh++ }) { Text("Retry") }
                }
            }
            if (!loading && error == null && visible.isEmpty()) item(key = "empty") {
                InfoCard(Modifier.fillMaxWidth()) { Text("No results"); SmallNote("Change the search or list filters.") }
            }
            items(visible, key = { "app:${it.packageName}" }, contentType = { "app" }) { app ->
                val checked = mode == AppMode.ALL || app.packageName in selectedSet
                val selectable = mode != AppMode.ALL && !busy && !loading && error == null
                InfoCard(Modifier.fillMaxWidth().toggleable(checked, enabled = selectable, role = Role.Checkbox,
                    onValueChange = { selected = AppSelection.toggle(selectedSet, app, catalog).toList() }),
                    accent = if (checked && mode != AppMode.ALL) Blue else null, contentPadding = 14.dp) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(13.dp)) {
                        InstalledAppIcon(app.packageName, catalogSource, refresh)
                        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                            Text(app.label, style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(app.packageName, color = Muted, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            if ((groupSizes[app.uid] ?: 0) > 1) Text("Linked applications: ${groupSizes[app.uid]}", color = Blue, style = MaterialTheme.typography.labelSmall)
                        }
                        Checkbox(checked = checked, enabled = selectable, onCheckedChange = null)
                    }
                }
            }
        }
        Row(Modifier.fillMaxWidth().padding(bottom = 10.dp), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            OutlinedButton(onClick = cancel, enabled = !busy, modifier = Modifier.weight(1f).heightIn(min = 48.dp), shape = MaterialTheme.shapes.medium) { Text("Cancel") }
            GradientButton(if (busy) "Saving…" else "Save", {
                keyboard?.hide()
                apply(AppPolicy(mode, if (mode == AppMode.ALL) emptyList() else selected.sorted()), catalog)
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
