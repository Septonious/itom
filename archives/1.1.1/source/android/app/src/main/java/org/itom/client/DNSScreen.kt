package org.itom.client

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import org.itom.domain.*

@Composable fun DNSScreen(config: PublicConfig, busy: Boolean, onSave: (DNSSettings) -> Unit) {
    var value by remember(config.revision) { mutableStateOf(config.settings.dnsResolver) }
    var error by remember { mutableStateOf<String?>(null) }
    val editable = !busy && !config.loading
    PageColumn {
        InfoCard(Modifier.fillMaxWidth()) {
            Text("DNS resolvers", style = MaterialTheme.typography.titleMedium)
            SmallNote("Both resolvers are queried together. The first answer wins, with a 250ms direct timeout.")
            OutlinedTextField(value.primary, { value = value.copy(primary = it.trim()); error = null },
                label = { Text("Primary resolver") }, singleLine = true, enabled = editable, modifier = Modifier.fillMaxWidth())
            OutlinedTextField(value.backup, { value = value.copy(backup = it.trim()); error = null },
                label = { Text("Backup resolver") }, singleLine = true, enabled = editable, modifier = Modifier.fillMaxWidth())
        }
        InfoCard(Modifier.fillMaxWidth()) {
            Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                Text("IMAP fallback", Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                ItomSwitch(value.itomFallback, { value = value.copy(itomFallback = it); error = null }, enabled = editable)
            }
            SmallNote(if (value.itomFallback) "If neither resolver answers, use IMAP DNS until you restart the VPN."
                else "DNS never uses mail capacity. Enable fallback to retry through ITOM only if both resolvers fail.")
        }
        TextButton({ value = DNSSettings(); error = null }, enabled = editable) { Text("Use defaults") }
        SmallNote("Saving restarts an active connection. Domain routing rules remain in effect.")
        error?.let { SmallNote(errorText(it), color = Red) }
        GradientButton("Save", {
            try { Validation.dns(value); onSave(value) }
            catch (e: DomainError) { error = e.code }
        }, enabled = editable, modifier = Modifier.fillMaxWidth())
    }
}
