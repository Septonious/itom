package org.itom.client

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

@Composable fun RulesScreen(policy: RoutingPolicy, appPolicy: AppPolicy, busy: Boolean, apps: () -> Unit, updateOptions: (RoutingPolicy) -> Unit = {}, apply: (RouteAction, List<String>) -> Unit) {
    var selected by rememberSaveable { mutableStateOf(RouteAction.VPN) }
    var vpn by rememberSaveable { mutableStateOf(policy.rules.VPN.joinToString("\n")) }
    var block by rememberSaveable { mutableStateOf(policy.rules.Block.joinToString("\n")) }
    var direct by rememberSaveable { mutableStateOf(policy.rules.Direct.joinToString("\n")) }
    var test by rememberSaveable { mutableStateOf("") }
    var result by remember { mutableStateOf("") }
    var checking by remember { mutableStateOf(false) }
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    LaunchedEffect(policy) { result = "" }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
        SmallNote("Block → VPN → Direct → GeoSite/GeoIP → VPN. First match wins.")
        InfoCard(Modifier.fillMaxWidth()) {
            Text("Automatic routing", style = MaterialTheme.typography.titleMedium)
            RoutingToggle("Russian websites", policy.ruSitesDirect, !busy) { updateOptions(policy.copy(ruSitesDirect = it)) }
            RoutingToggle("Russian IP addresses", policy.ruIpsDirect, !busy) { updateOptions(policy.copy(ruIpsDirect = it)) }
            Text("Blocking", style = MaterialTheme.typography.titleMedium)
            RoutingToggle("Ads and trackers", policy.blockAdsTrackers, !busy) { updateOptions(policy.copy(blockAdsTrackers = it)) }
            RoutingToggle("Malicious domains", policy.blockMalware, !busy) { updateOptions(policy.copy(blockMalware = it)) }
        }
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            listOf(RouteAction.BLOCK, RouteAction.VPN, RouteAction.DIRECT).forEach { action ->
                val active = selected == action
                InfoCard(Modifier.weight(1f).semantics { this.selected = active }, accent = if (active) routeColor(action) else null,
                    contentPadding = 12.dp, onClick = { selected = action }) {
                    Column(Modifier.fillMaxWidth(), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(6.dp)) {
                        RouteIcon(action, Modifier.size(25.dp))
                        Text(routeName(action), style = MaterialTheme.typography.titleMedium, color = if (active) routeColor(action) else Ink)
                        Text("${policy.rules.entries(action).size} rules", style = MaterialTheme.typography.labelSmall, color = Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                }
            }
        }
        val text = when (selected) { RouteAction.VPN -> vpn; RouteAction.BLOCK -> block; RouteAction.DIRECT -> direct }
        InfoCard(Modifier.fillMaxWidth()) {
            Text("Addresses · ${routeName(selected)}", style = MaterialTheme.typography.titleMedium)
            OutlinedTextField(value = text, onValueChange = {
                if (it.length <= 128 * 1024) when (selected) { RouteAction.VPN -> vpn = it; RouteAction.BLOCK -> block = it; RouteAction.DIRECT -> direct = it }
            }, label = { Text("One address per line") }, placeholder = { Text("google.com\n.ru\n192.0.2.0/24") }, minLines = 4, maxLines = 8,
                shape = MaterialTheme.shapes.medium, modifier = Modifier.fillMaxWidth())
            SmallNote("Domain or IP, domain:, full:, ip:, geosite: and geoip:ru. One rule per line.")
            GradientButton("Apply ${routeName(selected)}", { apply(selected, text.lines()) }, Modifier.fillMaxWidth(), enabled = !busy, glyph = Glyph.CHECK)
        }
        NavigationRow(Glyph.APPS, "Applications", appModeName(appPolicy.mode) + if (appPolicy.mode == AppMode.ALL) "" else " · ${appPolicy.packages.size}", apps)
        InfoCard(Modifier.fillMaxWidth()) {
            Text("Address check", style = MaterialTheme.typography.titleMedium)
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(value = test, onValueChange = { test = it; result = "" }, label = { Text("Domain or IP") }, singleLine = true,
                    shape = MaterialTheme.shapes.medium, modifier = Modifier.weight(1f))
                GlyphButton(Glyph.SEARCH, "Check saved rules", {
                    if (!checking) scope.launch {
                        checking = true
                        try { result = withContext(Dispatchers.IO) { NativeRouting.check(context, policy, test) } }
                        catch (e: CancellationException) { throw e }
                        catch (_: Exception) { result = "Invalid rule, target or routing data" }
                        finally { checking = false }
                    }
                }, Blue)
            }
            if (checking) LinearProgressIndicator(Modifier.fillMaxWidth())
            if (result.isNotEmpty()) Text(result, color = Blue)
            SmallNote("Saved rules are checked without opening a connection.")
        }
        Spacer(Modifier.height(4.dp))
    }
}

@Composable private fun RoutingToggle(name: String, checked: Boolean, enabled: Boolean, change: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        Text(name, Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
        Switch(checked = checked, enabled = enabled, onCheckedChange = change)
    }
}
