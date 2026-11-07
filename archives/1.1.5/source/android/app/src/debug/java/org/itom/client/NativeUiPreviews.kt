package org.itom.client

import androidx.compose.foundation.layout.*
import androidx.compose.material3.Scaffold
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.tooling.preview.Preview
import org.itom.domain.*

// Native Android Studio previews use the actual composables, with no HTML or production fixtures.
private fun previewConfig(): PublicConfig {
    val profile = ServerProfile(profileId = "preview-server", name = "Personal server",
        serverId = "0123456789abcdef0123456789abcdef", userId = "12345678", providerId = "rambler",
        encryptionKeyRef = "keystore:preview_key", mailboxNamespace = "preview.user.12345678",
        accounts = (1..10).map { MailAccount("preview-$it", "Account $it", "mail.example.invalid", credentialRef = "keystore:preview_$it", providerId = "rambler") })
    return PublicConfig(profiles = listOf(profile), selectedProfileId = profile.profileId, loading = false)
}

@Composable private fun PreviewHome(phase: Phase) {
    val config = previewConfig()
    val session = SessionSnapshot(phase = phase, profileId = config.selectedProfileId,
        metrics = if (phase == Phase.CONNECTED) Metrics(12_400_000, 3_100_000, 128, 8, 19, 20) else Metrics(totalLines = 20),
        transport = if (phase == Phase.CONNECTED) """{"ServerMessage":"Scheduled maintenance starts at 22:00 UTC. Your session will stay connected while we prepare the update."}""" else "{}",
        error = if (phase == Phase.ERROR) "CORE_NOT_INTEGRATED" else null,
        history = if (phase == Phase.CONNECTED) (1..24).map { Sample((8_000_000 + (it % 7) * 600_000).toLong(), (1_800_000 + (it % 5) * 200_000).toLong()) } else emptyList())
    ItomTheme {
        ItomBackdrop {
            Scaffold(containerColor = Color.Transparent, contentColor = Ink, topBar = { ItomTopBar(Page.HOME, {}, session) },
                bottomBar = { ItomBottomBar(Page.HOME) {} }) { padding ->
                Box(Modifier.fillMaxSize().padding(padding).consumeWindowInsets(padding)) {
                    HomeScreen(config, session, { 137f }, {}, {}, {})
                }
            }
        }
    }
}

@Preview(name = "Phone • Connected", widthDp = 393, heightDp = 852, showSystemUi = true)
@Composable private fun ConnectedPreview() = PreviewHome(Phase.CONNECTED)

@Preview(name = "Compact • Off", widthDp = 360, heightDp = 640, showSystemUi = true)
@Preview(name = "Small • Off", widthDp = 320, heightDp = 568, showSystemUi = true)
@Composable private fun OffPreview() = PreviewHome(Phase.OFF)

@Preview(name = "Landscape • Connecting", widthDp = 800, heightDp = 360, showSystemUi = true)
@Composable private fun ConnectingPreview() = PreviewHome(Phase.CONNECTING)

@Preview(name = "Error • Text 130%", widthDp = 360, heightDp = 740, fontScale = 1.3f, showSystemUi = true)
@Composable private fun ErrorPreview() = PreviewHome(Phase.ERROR)
