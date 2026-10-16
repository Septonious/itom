package org.itom.client

import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import kotlinx.serialization.json.JsonObject
import org.itom.domain.*

@Composable fun TransportLine(config: PublicConfig, session: SessionSnapshot, line: JsonObject) {
    val id = line.field("Account") ?: "—"
    val profile = config.profiles.find { it.profileId == session.profileId }
    val name = profile?.accounts?.find { it.id == id }?.name ?: id
    val color = when {
        session.phase == Phase.OFF -> Muted
        line.field("Ready") == "true" -> Green
        diagnosticLevel(line.field("LastError").orEmpty()) == DiagnosticLevel.ERROR -> Red
        else -> Yellow
    }
    fun rate(key: String): String = line.field(key)?.toLongOrNull()?.let(Presentation::rate) ?: "—"
    fun latency(key: String): String = line.field(key)?.toLongOrNull()?.takeIf { it > 0 }?.let { "$it ms" } ?: "—"
    InfoCard(Modifier.fillMaxWidth(), accent = color, contentPadding = 14.dp) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Text(name, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
            GlowDot(color)
        }
        Row(Modifier.fillMaxWidth()) {
            AccountMetric("Upload", rate("UpBps"), Green, Modifier.weight(1f))
            AccountMetric("Download", rate("DownBps"), Blue, Modifier.weight(1f))
        }
        Row(Modifier.fillMaxWidth()) {
            AccountMetric("Last RTT", latency("RTTMillis") + if (line.field("RTTFresh") == "false" && (line.field("RTTMillis")?.toLongOrNull() ?: 0L) > 0L) " (stale)" else "", color, Modifier.weight(1f))
            AccountMetric("IMAP latency", latency("IMAPLatencyMillis"), Ink, Modifier.weight(1f))
        }
        Row(Modifier.fillMaxWidth()) {
            AccountMetric("TCP streams", line.field("TCPStreams") ?: "—", Ink, Modifier.weight(1f))
            AccountMetric("Active lanes", "${line.field("ActiveLines") ?: "—"}/${line.field("TotalLines") ?: "—"}", color, Modifier.weight(1f))
        }
    }
}

@Composable private fun AccountMetric(label: String, value: String, color: Color, modifier: Modifier) {
    Column(modifier, verticalArrangement = Arrangement.spacedBy(2.dp)) {
        Text(label, color = Muted, style = MaterialTheme.typography.labelSmall)
        Text(value, color = color, style = MaterialTheme.typography.bodyMedium)
    }
}
