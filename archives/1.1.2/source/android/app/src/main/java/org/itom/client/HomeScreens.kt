package org.itom.client

import android.provider.Settings
import androidx.compose.animation.animateColorAsState
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.clickable
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalInspectionMode
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.repeatOnLifecycle
import kotlinx.coroutines.isActive
import org.itom.domain.*
import kotlin.math.cos
import kotlin.math.sin

@Composable fun HomeScreen(config: PublicConfig, session: SessionSnapshot, angle: () -> Float, onAngle: (Float) -> Unit, toggle: () -> Unit, profiles: () -> Unit) {
    val context = LocalContext.current
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val preview = LocalInspectionMode.current
    val latestAngle by rememberUpdatedState(angle)
    val latestUpdate by rememberUpdatedState(onAngle)
    LaunchedEffect(session.phase, lifecycle, preview) {
        if (session.phase != Phase.CONNECTING || preview) return@LaunchedEffect
        lifecycle.repeatOnLifecycle(Lifecycle.State.STARTED) {
            val scale = Settings.Global.getFloat(context.contentResolver, Settings.Global.ANIMATOR_DURATION_SCALE, 1f)
            if (scale <= 0f) return@repeatOnLifecycle
            var previous = 0L
            while (isActive) withFrameNanos { now ->
                if (previous != 0L) latestUpdate(Presentation.orbit(latestAngle(), now - previous, scale))
                previous = now
            }
        }
    }
    val color by animateColorAsState(phaseColor(session.phase), label = "connectionColor")
    // Only the hero receives the remaining height. Essential controls and counters stay on screen.
    BoxWithConstraints(Modifier.fillMaxSize().padding(horizontal = 20.dp, vertical = 6.dp)) {
        val availableWidth = maxWidth
        val availableHeight = maxHeight
        val wide = availableWidth >= 540.dp && availableWidth > availableHeight
        val compact = availableHeight < 570.dp || LocalDensity.current.fontScale > 1.2f
        val heroHeight = if (wide) 200.dp else minOf(availableWidth, 286.dp) + 62.dp * LocalDensity.current.fontScale.coerceAtLeast(1f)
        val controlsBudget = if (compact) 220.dp else 350.dp
        val scrollControls = availableHeight < heroHeight + controlsBudget || LocalDensity.current.fontScale > 1.3f
        val gap = if (compact) 8.dp else 12.dp
        if (wide) {
            Row(Modifier.fillMaxSize(), horizontalArrangement = Arrangement.spacedBy(22.dp), verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(.44f).fillMaxHeight().then(if (scrollControls) Modifier.verticalScroll(rememberScrollState()) else Modifier), verticalArrangement = Arrangement.spacedBy(gap)) {
                    ConnectionHero(session, color, angle, toggle, (if (scrollControls) Modifier.height(heroHeight) else Modifier.weight(1f)).fillMaxWidth())
                    ProfileSwitch(config, true, profiles)
                }
                Column(Modifier.weight(.56f), verticalArrangement = Arrangement.spacedBy(gap)) {
                    SpeedCards(session, true)
                    Vitals(config, session, true)
                }
            }
        } else {
            Column(Modifier.fillMaxSize().then(if (scrollControls) Modifier.verticalScroll(rememberScrollState()) else Modifier), verticalArrangement = Arrangement.spacedBy(gap)) {
                ConnectionHero(session, color, angle, toggle, (if (scrollControls) Modifier.height(heroHeight) else Modifier.weight(1f)).fillMaxWidth())
                ProfileSwitch(config, compact, profiles)
                if (!compact) Row(Modifier.fillMaxWidth().padding(horizontal = 2.dp), verticalAlignment = Alignment.CenterVertically) {
                    Text("Traffic", Modifier.weight(1f), style = MaterialTheme.typography.labelMedium, color = Muted)
                    GlowDot(if (session.phase == Phase.CONNECTED) color else Muted)
                    Text(if (session.phase == Phase.CONNECTED) "LIVE" else "IDLE", fontSize = 10.sp, color = Muted, letterSpacing = 1.sp)
                }
                SpeedCards(session, compact)
                Vitals(config, session, compact)
            }
        }
    }
}

@Composable private fun ConnectionHero(session: SessionSnapshot, color: Color, angle: () -> Float, toggle: () -> Unit, modifier: Modifier) {
    var showError by remember { mutableStateOf(false) }
    val errorCode = session.error
    val hint = when {
        session.error != null -> "Show disconnection reason"
        session.phase == Phase.CONNECTING -> startupStatus(session.transport)
        session.phase == Phase.CONNECTED -> "Tap to disconnect"
        session.phase == Phase.ERROR -> "Tap to retry"
        else -> "Tap the circle to connect"
    }
    BoxWithConstraints(modifier) {
        // Capture before entering Row/Column: their DSL scopes hide the outer implicit receiver.
        val availableWidth = maxWidth
        val availableHeight = maxHeight
        val fontScale = LocalDensity.current.fontScale
        val textBudget = 62.dp * fontScale.coerceAtLeast(1f)
        val horizontal = availableHeight < textBudget + 88.dp
        val compactOrbSize = minOf(availableHeight, availableWidth * .35f).coerceAtLeast(48.dp)
        @Composable fun Label(modifier: Modifier = Modifier) {
            Column(modifier.then(if (session.error != null) Modifier.heightIn(min = 48.dp)
                .clickable(role = Role.Button, onClickLabel = "Show disconnection reason", onClick = { showError = true }) else Modifier),
                horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(3.dp)) {
                FitText(phaseName(session.phase), Modifier.fillMaxWidth(), color, maxSize = 24, minSize = 14, align = TextAlign.Center)
                Text(hint, color = if (session.error != null) Red else Muted,
                    fontSize = 10.sp, lineHeight = 14.sp, textAlign = TextAlign.Center, maxLines = 2, overflow = TextOverflow.Ellipsis)
            }
        }
        if (horizontal) {
            Row(Modifier.fillMaxSize(), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                PowerOrb(session.phase, color, angle, toggle, Modifier.size(compactOrbSize))
                Label(Modifier.weight(1f))
            }
        } else {
            val ringSize = minOf(availableWidth, (availableHeight - textBudget).coerceAtLeast(48.dp), 286.dp)
            Column(Modifier.fillMaxSize(), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
                PowerOrb(session.phase, color, angle, toggle, Modifier.size(ringSize))
                Label(Modifier.fillMaxWidth())
            }
        }
    }
    if (showError && errorCode != null) AlertDialog(onDismissRequest = { showError = false }, title = { Text("Disconnection reason") },
        text = { Column(verticalArrangement = Arrangement.spacedBy(8.dp)) { DiagnosticCode(errorCode, terminal = true); Text(errorText(errorCode)) } }, confirmButton = { TextButton(onClick = { showError = false }) { Text("OK") } })
}

@Composable private fun PowerOrb(phase: Phase, color: Color, angle: () -> Float, toggle: () -> Unit, modifier: Modifier) {
    val action = when (phase) { Phase.CONNECTING -> "Cancel connection"; Phase.CONNECTED -> "Disconnect"; else -> "Connect" }
    Box(modifier.clip(CircleShape).semantics { contentDescription = action; stateDescription = phaseName(phase) }
        .clickable(role = Role.Button, onClickLabel = action, onClick = toggle), contentAlignment = Alignment.Center) {
        Canvas(Modifier.fillMaxSize()) {
            val radius = size.minDimension * .33f
            val glow = size.minDimension * .5f
            // Analytic radial falloff works on minSdk 29 without a full-screen blur layer.
            val intensity = if (phase == Phase.OFF) .55f else 1f
            drawCircle(Brush.radialGradient(0f to color.copy(alpha = .015f * intensity),
                .35f to color.copy(alpha = .025f * intensity), .5f to color.copy(alpha = .055f * intensity),
                .62f to color.copy(alpha = .11f * intensity), .7f to color.copy(alpha = .10f * intensity),
                .82f to color.copy(alpha = .045f * intensity), .92f to color.copy(alpha = .01f * intensity),
                1f to Color.Transparent, center = center, radius = glow), glow)
            drawCircle(Brush.radialGradient(listOf(Color(0xFF172F41), Color(0xFF091923)), center, radius), radius * .96f)
            drawCircle(color.copy(alpha = .13f), radius * .89f, style = Stroke(1.dp.toPx()))
            drawCircle(Brush.sweepGradient(listOf(color.copy(alpha = .55f), color, color.copy(alpha = .65f), color.copy(alpha = .55f)), center), radius, style = Stroke(3.5.dp.toPx()))
            drawArc(Color.White.copy(alpha = .2f), 214f, 54f, false,
                center - Offset(radius, radius), androidx.compose.ui.geometry.Size(radius * 2, radius * 2), style = Stroke(1.dp.toPx()))
            // Read the angle during drawing: animation invalidates the canvas, not the whole UI tree.
            val radians = angle() * Math.PI / 180
            val ball = center + Offset((cos(radians) * radius).toFloat(), (sin(radians) * radius).toFloat())
            val ballRadius = (radius * .072f).coerceIn(3.dp.toPx(), 8.dp.toPx())
            drawCircle(Brush.radialGradient(listOf(color.copy(alpha = .28f), color.copy(alpha = .06f), Color.Transparent), ball, ballRadius * 5.5f), ballRadius * 5.5f, ball)
            drawCircle(Background, ballRadius + 2.5.dp.toPx(), ball)
            drawCircle(color, ballRadius, ball)
            drawCircle(Color.White.copy(alpha = .4f), ballRadius * .26f, ball - Offset(ballRadius * .25f, ballRadius * .25f))
        }
        AppIcon(Glyph.MAIL, Modifier.fillMaxSize(.27f), Color(0xFFB9D0E2))
    }
}

@Composable private fun ProfileSwitch(config: PublicConfig, compact: Boolean, onClick: () -> Unit) {
    val profile = config.selected
    InfoCard(Modifier.fillMaxWidth(), contentPadding = if (compact) 12.dp else 16.dp, onClick = onClick) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            IconBadge(if (profile == null) Glyph.PLUS else Glyph.SERVERS, modifier = Modifier.size(if (compact) 36.dp else 42.dp))
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(2.dp)) {
                Text(profile?.name ?: "Add profile", style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Text(profile?.let { "${it.providerId} · ID ${it.userId}" } ?: "Server invitation", style = MaterialTheme.typography.bodySmall, color = Muted, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            AppIcon(Glyph.NEXT, Modifier.size(18.dp))
        }
    }
}

@Composable private fun SpeedCards(session: SessionSnapshot, compact: Boolean) {
    Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.spacedBy(10.dp)) {
        SpeedCard("Download", session.metrics.downBps, Glyph.DOWN, Blue, session, true, compact, Modifier.weight(1f))
        SpeedCard("Upload", session.metrics.upBps, Glyph.UP, Green, session, false, compact, Modifier.weight(1f))
    }
}

@Composable private fun SpeedCard(label: String, bps: Long, glyph: Glyph, color: Color, session: SessionSnapshot, down: Boolean, compact: Boolean, modifier: Modifier) {
    val rate = Presentation.rate(bps)
    InfoCard(modifier, contentPadding = if (compact) 12.dp else 16.dp) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(7.dp)) {
            AppIcon(glyph, Modifier.size(17.dp), color)
            Text(label, color = Muted, style = MaterialTheme.typography.labelSmall, maxLines = 1)
        }
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.Bottom, horizontalArrangement = Arrangement.spacedBy(4.dp)) {
            FitText(rate.substringBefore(' '), Modifier.weight(1f), maxSize = if (compact) 26 else 32)
            Text(rate.substringAfter(' '), color = Muted, fontSize = 10.sp, modifier = Modifier.padding(bottom = 3.dp))
        }
        if (!compact) Sparkline(session.history.takeLast(24), down, color, Modifier.fillMaxWidth().height(23.dp))
    }
}

@Composable private fun Vitals(config: PublicConfig, session: SessionSnapshot, compact: Boolean) {
    val lines = "${session.metrics.activeLines}/${if (session.profileId != null) session.metrics.totalLines else config.selected?.totalLines ?: 0}"
    InfoCard(Modifier.fillMaxWidth(), contentPadding = if (compact) 10.dp else 14.dp) {
        Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
            Vital("RTT", Presentation.rtt(session.metrics.rttMs), rttColor(session.metrics.rttMs, config.settings.rttThresholds), Modifier.weight(1f))
            VerticalDivider(Modifier.height(28.dp), color = Outline)
            Vital("TCP streams", session.metrics.tcp.toString(), Ink, Modifier.weight(1f))
            VerticalDivider(Modifier.height(28.dp), color = Outline)
            Vital("Active lanes", lines, Ink, Modifier.weight(1.25f))
        }
    }
}

@Composable private fun Vital(label: String, value: String, color: Color, modifier: Modifier) {
    Column(modifier.padding(horizontal = 3.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(4.dp)) {
        Text(label, color = Muted, fontSize = 10.sp, lineHeight = 13.sp, maxLines = 1, overflow = TextOverflow.Ellipsis)
        FitText(value, Modifier.fillMaxWidth(), color, maxSize = 21, minSize = 13, align = TextAlign.Center)
    }
}

@Composable private fun Sparkline(samples: List<Sample>, download: Boolean, color: Color, modifier: Modifier) {
    Canvas(modifier) {
        if (samples.size < 2) {
            drawLine(color.copy(alpha = .25f), Offset(0f, size.height * .8f), Offset(size.width, size.height * .8f), 1.dp.toPx())
            return@Canvas
        }
        val max = samples.maxOf { if (download) it.downBps else it.upBps }.coerceAtLeast(1).toFloat()
        val path = Path()
        samples.forEachIndexed { i, s ->
            val x = size.width * i / (samples.size - 1)
            val y = size.height * (.9f - (if (download) s.downBps else s.upBps) / max * .75f)
            if (i == 0) path.moveTo(x, y) else path.lineTo(x, y)
        }
        val fill = Path().apply { addPath(path); lineTo(size.width, size.height); lineTo(0f, size.height); close() }
        drawPath(fill, Brush.verticalGradient(listOf(color.copy(alpha = .18f), Color.Transparent)))
        drawPath(path, color.copy(alpha = .09f), style = Stroke(6.dp.toPx(), cap = StrokeCap.Round))
        drawPath(path, color, style = Stroke(1.5.dp.toPx(), cap = StrokeCap.Round))
    }
}

@Composable fun TransportChart(session: SessionSnapshot) {
        InfoCard(Modifier.fillMaxWidth()) {
            Text("Throughput", style = MaterialTheme.typography.titleMedium)
            if (session.history.isEmpty()) SmallNote("Data appears after connecting.")
            Canvas(Modifier.fillMaxWidth().height(120.dp)) {
                for (i in 0..3) drawLine(Muted.copy(alpha = .14f), Offset(0f, size.height * i / 3), Offset(size.width, size.height * i / 3), 1.dp.toPx())
                val max = session.history.maxOfOrNull { maxOf(it.downBps, it.upBps) }?.coerceAtLeast(1)?.toFloat() ?: 1f
                fun line(download: Boolean, color: Color) {
                    if (session.history.size < 2) return
                    val path = Path()
                    session.history.forEachIndexed { i, s ->
                        val x = size.width * i / (session.history.size - 1)
                        val y = size.height * (1 - (if (download) s.downBps else s.upBps) / max) * .9f + size.height * .05f
                        if (i == 0) path.moveTo(x, y) else path.lineTo(x, y)
                    }
                    drawPath(path, color.copy(alpha = .08f), style = Stroke(8.dp.toPx()))
                    drawPath(path, color, style = Stroke(2.dp.toPx()))
                }
                line(true, Blue); line(false, Green)
            }
            Text("↓ ${Presentation.rate(session.metrics.downBps)}", color = Blue)
            Text("↑ ${Presentation.rate(session.metrics.upBps)}", color = Green)
            SmallNote("RTT ${Presentation.rtt(session.metrics.rttMs)} · TCP ${session.metrics.tcp} · lanes ${session.metrics.activeLines}/${session.metrics.totalLines}")
        }
}
