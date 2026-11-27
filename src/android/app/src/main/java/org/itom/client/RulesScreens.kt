package org.itom.client

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.CancellationException
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import org.itom.domain.*

@Composable fun RulesScreen(policy: RoutingPolicy, appPolicy: AppPolicy, busy: Boolean, apps: () -> Unit, updateOptions: (RoutingPolicy) -> Unit = {}, updateApps: (Boolean) -> Unit = {}, importRules: (AddressRules) -> Unit, onMessage: (String) -> Unit, apply: (RouteAction, List<String>) -> Unit) {
    var selected by rememberSaveable { mutableStateOf<RouteAction?>(null) }
    var draft by rememberSaveable { mutableStateOf("") }
    var destination by rememberSaveable { mutableStateOf(false) }
    var test by rememberSaveable { mutableStateOf("") }
    var result by remember { mutableStateOf("") }
    var checking by remember { mutableStateOf(false) }
    var updating by remember { mutableStateOf(false) }
    var updated by remember { mutableStateOf("") }
    var incoming by remember { mutableStateOf<AddressRules?>(null) }
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    LaunchedEffect(policy) { result = "" }
    LaunchedEffect(Unit) { updated = withContext(Dispatchers.IO) { NativeRouting.updatedLabel(context) } }
    val importPicker = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri ->
        if (uri != null) scope.launch {
            try { incoming = RoutingRulesExchange.decode(readBounded(context, uri, RoutingRulesExchange.LIMIT).toString(Charsets.UTF_8)) }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { onMessage("Invalid routing rule document.") }
        }
    }
    val exportPicker = rememberLauncherForActivityResult(ActivityResultContracts.CreateDocument("application/json")) { uri ->
        if (uri != null) scope.launch {
            try {
                val bytes = RoutingRulesExchange.encode(policy.rules).toByteArray(Charsets.UTF_8)
                withContext(Dispatchers.IO) {
                    context.contentResolver.openOutputStream(uri, "wt")?.use { it.write(bytes) } ?: throw DomainError("FILE_UNAVAILABLE")
                }
                onMessage("Custom rules exported.")
            } catch (e: CancellationException) { throw e }
            catch (_: Exception) { onMessage("Could not export rules.") }
        }
    }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
        SmallNote("Applies to all profiles. Changes apply on the next connection.")
        InfoCard(Modifier.fillMaxWidth(), contentPadding = 12.dp) {
            Text("Direct", style = MaterialTheme.typography.titleMedium)
            RoutingToggle("Russian sites", "GeoSite and .ru or .рф domains", policy.ruSitesDirect, !busy) { updateOptions(policy.copy(ruSitesDirect = it)) }
            RoutingToggle("Russian IP addresses", "IPv4 and IPv6 ranges in GeoIP", policy.ruIpsDirect, !busy) { updateOptions(policy.copy(ruIpsDirect = it)) }
            RoutingToggle("Russian applications", "Automatically bypass the VPN for recognized apps", appPolicy.autoRussian, !busy) { updateApps(it) }
        }
        InfoCard(Modifier.fillMaxWidth(), contentPadding = 12.dp, onClick = apps) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                IconBadge(Glyph.APPS)
                Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                    Text("Applications", style = MaterialTheme.typography.titleMedium)
                    SmallNote("${appPolicy.directPackages.size} direct · ${appPolicy.vpnPackages.size} VPN")
                }
                AppIcon(Glyph.NEXT, Modifier.size(18.dp))
            }
        }
        InfoCard(Modifier.fillMaxWidth(), contentPadding = 12.dp) {
            Text("Blocking", style = MaterialTheme.typography.titleMedium)
            RoutingToggle("Ads and trackers", "GeoSite and HaGeZi blocklists", policy.blockAdsTrackers, !busy) { updateOptions(policy.copy(blockAdsTrackers = it)) }
            RoutingToggle("Malicious sites", "HaGeZi threat intelligence list", policy.blockMalware, !busy) { updateOptions(policy.copy(blockMalware = it)) }
        }
        InfoCard(Modifier.fillMaxWidth(), contentPadding = 12.dp) {
            Text("Custom rules", style = MaterialTheme.typography.titleMedium)
            SmallNote("Block takes priority, then VPN, then Direct. Unmatched traffic uses VPN.")
            listOf(RouteAction.BLOCK, RouteAction.VPN, RouteAction.DIRECT).forEach { action ->
                OutlinedButton(onClick = { draft = policy.rules.entries(action).joinToString("\n"); selected = action }, modifier = Modifier.fillMaxWidth(), enabled = !busy && !updating) {
                    Text(routeName(action), color = routeColor(action), modifier = Modifier.weight(1f))
                    Text("${policy.rules.entries(action).size} rules", color = Muted)
                    AppIcon(Glyph.NEXT, Modifier.size(18.dp))
                }
            }
            TextButton(onClick = { destination = true }, enabled = !updating) { Text("Check a destination", color = Blue) }
        }
        InfoCard(Modifier.fillMaxWidth(), contentPadding = 12.dp) {
            Text("GeoSite and GeoIP", style = MaterialTheme.typography.titleMedium)
            SmallNote(updated.ifBlank { "Reading dataset information…" })
            OutlinedButton(enabled = !busy && !updating, onClick = {
                updating = true
                scope.launch {
                    try {
                        updated = withContext(Dispatchers.IO) { NativeRouting.updateLists(context, policy); NativeRouting.updatedLabel(context) }
                        onMessage("Routing lists updated. Apply on next connection.")
                    } catch (e: CancellationException) { throw e }
                    catch (_: Exception) { onMessage("Update failed. Previous lists retained.") }
                    finally { updating = false }
                }
            }) { Text(if (updating) "Updating…" else "Update lists") }
            if (updating) LinearProgressIndicator(Modifier.fillMaxWidth())
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
            OutlinedButton(onClick = { importPicker.launch(arrayOf("application/json", "text/plain", "application/octet-stream")) }, enabled = !busy && !updating, modifier = Modifier.weight(1f)) { Text("Import rules") }
            OutlinedButton(onClick = { exportPicker.launch("itom-routing-rules.json") }, enabled = !busy, modifier = Modifier.weight(1f)) { Text("Export rules") }
        }
        Spacer(Modifier.height(4.dp))
    }
    selected?.let { action ->
        AlertDialog(onDismissRequest = { selected = null }, title = { Text("${routeName(action)} rules") }, text = {
            Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(draft, { if (it.length <= 128 * 1024) draft = it }, label = { Text("One address per line") }, placeholder = { Text("google.com\n.ru\n192.0.2.0/24") }, minLines = 4, maxLines = 8, modifier = Modifier.fillMaxWidth())
                SmallNote("Domain or IP, domain:, full:, ip:, geosite: and geoip:ru.")
            }
        }, confirmButton = { TextButton(enabled = !busy, onClick = { apply(action, draft.lines()); selected = null }) { Text("Save") } }, dismissButton = { TextButton(onClick = { selected = null }) { Text("Cancel") } })
    }
    if (destination) AlertDialog(onDismissRequest = { destination = false }, title = { Text("Check a destination") }, text = {
        Column(verticalArrangement = Arrangement.spacedBy(8.dp)) {
            OutlinedTextField(test, { test = it; result = "" }, label = { Text("Domain or IP") }, singleLine = true, modifier = Modifier.fillMaxWidth())
            if (checking) LinearProgressIndicator(Modifier.fillMaxWidth())
            if (result.isNotEmpty()) Text(result, color = Blue)
            SmallNote("Checks saved rules without opening a connection.")
        }
    }, confirmButton = { TextButton(enabled = !checking && test.isNotBlank(), onClick = {
        checking = true
        scope.launch {
            try { result = withContext(Dispatchers.IO) { NativeRouting.check(context, policy, test) } }
            catch (e: CancellationException) { throw e }
            catch (_: Exception) { result = "Invalid target or routing data" }
            finally { checking = false }
        }
    }) { Text("Check") } }, dismissButton = { TextButton(onClick = { destination = false }) { Text("Close") } })
    incoming?.let { rules -> AlertDialog(onDismissRequest = { incoming = null }, title = { Text("Replace custom rules?") },
        text = { Text("Import ${rules.Block.size} Block, ${rules.VPN.size} VPN and ${rules.Direct.size} Direct rules. Existing custom rules will be replaced. Other settings are unchanged.") },
        confirmButton = { TextButton(enabled = !busy, onClick = { importRules(rules); incoming = null }) { Text("Import") } },
        dismissButton = { TextButton(onClick = { incoming = null }) { Text("Cancel") } }) }

}

@Composable private fun RoutingToggle(name: String, description: String, checked: Boolean, enabled: Boolean, change: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().heightIn(min = 48.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
            Text(name, style = MaterialTheme.typography.bodyMedium)
            SmallNote(description)
        }
        ItomSwitch(checked = checked, enabled = enabled, onCheckedChange = change)
    }
}
