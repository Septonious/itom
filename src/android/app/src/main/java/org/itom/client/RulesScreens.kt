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

@Composable fun RulesScreen(policy: RoutingPolicy, appPolicy: AppPolicy, busy: Boolean, apps: () -> Unit, updateOptions: (RoutingPolicy) -> Unit = {}, updateApps: (Boolean) -> Unit = {}, apply: (RouteAction, List<String>) -> Unit) {
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
        SmallNote("Applies to all profiles. Changes apply on the next connection.")
        InfoCard(Modifier.fillMaxWidth()) {
            Text("Direct", style = MaterialTheme.typography.titleMedium)
            RoutingToggle("Russian sites", policy.ruSitesDirect, !busy) { updateOptions(policy.copy(ruSitesDirect = it)) }
            SmallNote("GeoSite and .ru or .рф domains")
            RoutingToggle("Russian IP addresses", policy.ruIpsDirect, !busy) { updateOptions(policy.copy(ruIpsDirect = it)) }
            SmallNote("IPv4 and IPv6 ranges in GeoIP")
            RoutingToggle("Russian applications", appPolicy.autoRussian, !busy) { updateApps(it) }
            SmallNote("Automatically bypass the VPN for recognized apps")
        }
        NavigationRow(Glyph.APPS, "Applications", "${appPolicy.directPackages.size} direct · ${appPolicy.vpnPackages.size} VPN", apps)
        InfoCard(Modifier.fillMaxWidth()) {
            Text("Blocking", style = MaterialTheme.typography.titleMedium)
            RoutingToggle("Ads and trackers", policy.blockAdsTrackers, !busy) { updateOptions(policy.copy(blockAdsTrackers = it)) }
            SmallNote("GeoSite and HaGeZi blocklists")
            RoutingToggle("Malicious sites", policy.blockMalware, !busy) { updateOptions(policy.copy(blockMalware = it)) }
            SmallNote("HaGeZi threat intelligence list")
        }
        InfoCard(Modifier.fillMaxWidth()) {
            Text("Custom rules", style = MaterialTheme.typography.titleMedium)
            SmallNote("Block takes priority, then VPN, then Direct. Unmatched traffic uses VPN.")
            listOf(RouteAction.BLOCK, RouteAction.VPN, RouteAction.DIRECT).forEach { action ->
                val active = selected == action
                OutlinedButton(onClick = { selected = action }, modifier = Modifier.fillMaxWidth().semantics { this.selected = active }) {
                    Text(routeName(action), color = routeColor(action), modifier = Modifier.weight(1f))
                    Text("${policy.rules.entries(action).size} rules", color = Muted)
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
        InfoCard(Modifier.fillMaxWidth()) {
            Text("Check a destination", style = MaterialTheme.typography.titleMedium)
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
        ItomSwitch(checked = checked, enabled = enabled, onCheckedChange = change)
    }
}
