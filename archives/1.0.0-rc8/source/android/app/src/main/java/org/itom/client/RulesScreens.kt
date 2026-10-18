package org.itom.client

import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import org.itom.domain.*

@Composable fun RulesScreen(policy: RoutingPolicy, appPolicy: AppPolicy, busy: Boolean, apps: () -> Unit, apply: (RouteAction, List<String>) -> Unit) {
    var selected by rememberSaveable { mutableStateOf(RouteAction.VPN) }
    var vpn by rememberSaveable { mutableStateOf(policy.rules.VPN.joinToString("\n")) }
    var block by rememberSaveable { mutableStateOf(policy.rules.Block.joinToString("\n")) }
    var direct by rememberSaveable { mutableStateOf(policy.rules.Direct.joinToString("\n")) }
    var test by rememberSaveable { mutableStateOf("") }
    var result by remember { mutableStateOf("") }
    LaunchedEffect(policy) { result = "" }
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(14.dp)) {
        SmallNote("VPN → Block → Direct. The first match wins. Unmatched traffic uses VPN.")
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            listOf(RouteAction.VPN, RouteAction.BLOCK, RouteAction.DIRECT).forEach { action ->
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
            SmallNote("Domains include subdomains. Domain suffixes (.ru), IP addresses and CIDR ranges are supported.")
            GradientButton("Apply ${routeName(selected)}", { apply(selected, text.lines()) }, Modifier.fillMaxWidth(), enabled = !busy, glyph = Glyph.CHECK)
        }
        NavigationRow(Glyph.APPS, "Applications", appModeName(appPolicy.mode) + if (appPolicy.mode == AppMode.ALL) "" else " · ${appPolicy.packages.size}", apps)
        InfoCard(Modifier.fillMaxWidth()) {
            Text("Address check", style = MaterialTheme.typography.titleMedium)
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedTextField(value = test, onValueChange = { test = it; result = "" }, label = { Text("Domain or IP") }, singleLine = true,
                    shape = MaterialTheme.shapes.medium, modifier = Modifier.weight(1f))
                GlyphButton(Glyph.SEARCH, "Check saved rules", {
                    result = try { val r = Router(policy).decide(host = test.trim()); "${routeName(r.action)} · ${r.matched ?: "default rule"}" }
                    catch (_: DomainError) { "Invalid domain or IP" }
                }, Blue)
            }
            if (result.isNotEmpty()) Text(result, color = Blue)
            SmallNote("Saved rules are checked without opening a connection.")
        }
        Spacer(Modifier.height(4.dp))
    }
}
