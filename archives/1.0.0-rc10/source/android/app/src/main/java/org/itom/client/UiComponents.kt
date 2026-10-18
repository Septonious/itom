package org.itom.client

import androidx.compose.animation.animateColorAsState
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.remember
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.StrokeCap
import androidx.compose.ui.graphics.StrokeJoin
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.drawscope.scale
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import org.itom.domain.RouteAction
import org.itom.domain.Phase
import org.itom.domain.SessionSnapshot
import org.itom.domain.TransportDiagnostics
import org.itom.domain.field

enum class Glyph { HOME, SERVERS, ROUTES, SETTINGS, APPS, BELL, CHART, BACK, NEXT, PLUS, MAIL, SEARCH, CHECK, REFRESH, DOWN, UP, CLOSE, NETWORK }

/** Small native vector set: no font-dependent Unicode icons or bitmap decoration. */
@Composable fun AppIcon(glyph: Glyph, modifier: Modifier = Modifier, color: Color = Muted) {
    Canvas(modifier.size(24.dp)) {
        val stroke = Stroke(1.7f, cap = StrokeCap.Round, join = StrokeJoin.Round)
        scale(size.width / 24f, size.height / 24f, pivot = Offset.Zero) {
            fun line(x1: Float, y1: Float, x2: Float, y2: Float) = drawLine(color, Offset(x1, y1), Offset(x2, y2), 1.7f, StrokeCap.Round)
            fun path(vararg xy: Float) {
                val p = Path(); p.moveTo(xy[0], xy[1]); var i = 2
                while (i < xy.size) { p.lineTo(xy[i], xy[i + 1]); i += 2 }; drawPath(p, color, style = stroke)
            }
            fun rect(x: Float, y: Float, w: Float, h: Float, r: Float = 2f) = drawRoundRect(color, Offset(x, y), Size(w, h), CornerRadius(r), style = stroke)
            when (glyph) {
                Glyph.HOME -> { path(3f, 11f, 12f, 3f, 21f, 11f); path(6f, 10f, 6f, 21f, 10f, 21f, 10f, 15f, 14f, 15f, 14f, 21f, 18f, 21f, 18f, 10f) }
                Glyph.SERVERS -> { rect(3f, 3f, 18f, 7f); rect(3f, 14f, 18f, 7f); line(11f, 6.5f, 17f, 6.5f); line(11f, 17.5f, 17f, 17.5f); drawCircle(color, 1f, Offset(7f, 6.5f)); drawCircle(color, 1f, Offset(7f, 17.5f)) }
                Glyph.MAIL -> { rect(3f, 5f, 18f, 14f); path(4f, 7f, 12f, 13f, 20f, 7f) }
                Glyph.BACK -> path(15f, 5f, 8f, 12f, 15f, 19f)
                Glyph.NEXT -> path(9f, 5f, 16f, 12f, 9f, 19f)
                Glyph.PLUS -> { line(12f, 5f, 12f, 19f); line(5f, 12f, 19f, 12f) }
                Glyph.CLOSE -> { line(5f, 5f, 19f, 19f); line(19f, 5f, 5f, 19f) }
                Glyph.CHECK -> path(4f, 12f, 9f, 17f, 20f, 6f)
                Glyph.DOWN -> { line(12f, 3f, 12f, 21f); path(5f, 14f, 12f, 21f, 19f, 14f) }
                Glyph.UP -> { line(12f, 21f, 12f, 3f); path(5f, 10f, 12f, 3f, 19f, 10f) }
                Glyph.SEARCH -> { drawCircle(color, 6.5f, Offset(10f, 10f), style = stroke); line(15f, 15f, 21f, 21f) }
                Glyph.APPS -> { rect(3f, 3f, 7f, 7f, 1.5f); rect(14f, 3f, 7f, 7f, 1.5f); rect(3f, 14f, 7f, 7f, 1.5f); rect(14f, 14f, 7f, 7f, 1.5f) }
                Glyph.BELL -> { val p = Path().apply { moveTo(5f, 17f); lineTo(7f, 14f); lineTo(7f, 9f); cubicTo(7f, 2f, 17f, 2f, 17f, 9f); lineTo(17f, 14f); lineTo(19f, 17f); close() }; drawPath(p, color, style = stroke); line(10f, 21f, 14f, 21f) }
                Glyph.CHART -> { path(3f, 3f, 3f, 21f, 21f, 21f); path(6f, 15f, 10f, 10f, 14f, 13f, 21f, 5f) }
                Glyph.ROUTES -> { path(4f, 5f, 8f, 5f, 16f, 19f, 20f, 19f); path(17f, 16f, 20f, 19f, 17f, 22f); path(4f, 19f, 8f, 19f, 16f, 5f, 20f, 5f); path(17f, 2f, 20f, 5f, 17f, 8f) }
                Glyph.SETTINGS -> { val p = Path().apply { moveTo(9f, 3f); lineTo(15f, 3f); lineTo(16f, 6f); lineTo(20f, 8f); lineTo(22f, 12f); lineTo(20f, 16f); lineTo(16f, 18f); lineTo(15f, 21f); lineTo(9f, 21f); lineTo(8f, 18f); lineTo(4f, 16f); lineTo(2f, 12f); lineTo(4f, 8f); lineTo(8f, 6f); close() }; drawPath(p, color, style = stroke); drawCircle(color, 3.5f, Offset(12f, 12f), style = stroke) }
                Glyph.NETWORK -> { drawCircle(color, 9f, Offset(12f, 12f), style = stroke); drawOval(color, Offset(8f, 3f), Size(8f, 18f), style = stroke); line(3f, 12f, 21f, 12f) }
                Glyph.REFRESH -> { drawArc(color, 40f, 285f, false, Offset(4f, 4f), Size(16f, 16f), style = stroke); path(16f, 3f, 20f, 7f, 21f, 2f) }
            }
        }
    }
}

@Composable fun RouteIcon(action: RouteAction, modifier: Modifier = Modifier) {
    AppIcon(when (action) { RouteAction.VPN -> Glyph.NETWORK; RouteAction.BLOCK -> Glyph.CLOSE; RouteAction.DIRECT -> Glyph.UP }, modifier, routeColor(action))
}

@Composable fun IconBadge(glyph: Glyph, modifier: Modifier = Modifier, color: Color = Blue) {
    Box(modifier.size(44.dp).clip(CircleShape).background(Brush.linearGradient(listOf(color.copy(alpha = .22f), color.copy(alpha = .045f))))
        .border(1.dp, Brush.linearGradient(listOf(color.copy(alpha = .45f), color.copy(alpha = .12f))), CircleShape), contentAlignment = Alignment.Center) {
        AppIcon(glyph, color = color)
    }
}

@Composable fun GlowDot(color: Color, modifier: Modifier = Modifier) {
    Canvas(modifier.size(16.dp)) {
        drawCircle(Brush.radialGradient(listOf(color.copy(alpha = .4f), Color.Transparent), center, size.minDimension / 2))
        drawCircle(color, size.minDimension * .18f)
    }
}

@Composable fun GlyphButton(glyph: Glyph, description: String, onClick: () -> Unit, color: Color = Muted, enabled: Boolean = true) {
    IconButton(onClick = onClick, enabled = enabled, modifier = Modifier.semantics { contentDescription = description }) {
        AppIcon(glyph, color = if (enabled) color else color.copy(alpha = .4f))
    }
}

@Composable fun GradientButton(text: String, onClick: () -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true, glyph: Glyph? = null) {
    val shape = RoundedCornerShape(16.dp)
    Button(onClick, modifier.heightIn(min = 48.dp).clip(shape).background(Brush.linearGradient(
        if (enabled) listOf(ActionBlue, Color(0xFF4A98FF)) else listOf(Panel, Panel))),
        enabled = enabled, shape = shape, colors = ButtonDefaults.buttonColors(containerColor = Color.Transparent, contentColor = OnAccent, disabledContainerColor = Color.Transparent, disabledContentColor = Muted),
        border = BorderStroke(1.dp, if (enabled) Blue.copy(alpha = .55f) else Outline), contentPadding = PaddingValues(horizontal = 16.dp, vertical = 12.dp)) {
        if (glyph != null) { AppIcon(glyph, Modifier.size(18.dp), if (enabled) OnAccent else Muted); Spacer(Modifier.width(8.dp)) }
        Text(text, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}

/** Single-track, gapless determinate bar. No stop-marker, detached end cap or gap. */
@Composable fun ItomProgressBar(progress: Float, modifier: Modifier = Modifier, color: Color = Blue) {
    val value = if (progress.isFinite()) progress.coerceIn(0f, 1f) else 0f
    val shape = RoundedCornerShape(6.dp)
    Box(modifier.height(8.dp).clip(shape).background(Color(0xFF24334B))) {
        if (value > 0f) {
            Box(Modifier.fillMaxWidth(value).fillMaxHeight().background(color, shape))
        }
    }
}

@Composable fun NavigationRow(glyph: Glyph, title: String, subtitle: String, onClick: () -> Unit) {
    InfoCard(Modifier.fillMaxWidth(), onClick = onClick) {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(12.dp)) {
            IconBadge(glyph)
            Column(Modifier.weight(1f), verticalArrangement = Arrangement.spacedBy(3.dp)) {
                Text(title, style = MaterialTheme.typography.titleMedium)
                SmallNote(subtitle)
            }
            AppIcon(Glyph.NEXT, Modifier.size(18.dp))
        }
    }
}

@Composable fun ItomTopBar(page: Page, back: () -> Unit, session: SessionSnapshot? = null) {
    val isRoot = page in listOf(Page.HOME, Page.PROFILES, Page.RULES, Page.SETTINGS)
    Row(Modifier.windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Top + WindowInsetsSides.Horizontal))
        .fillMaxWidth().heightIn(min = 64.dp).padding(horizontal = 12.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
        if (!isRoot) GlyphButton(Glyph.BACK, "Back", back, Blue) else Spacer(Modifier.width(8.dp))
        if (page == Page.HOME) {
            Text("ITOM", fontSize = 28.sp, fontWeight = FontWeight.Medium, letterSpacing = 3.sp,
                maxLines = 1, modifier = Modifier.padding(end = 12.dp))
            ServerMessageHeader(session ?: SessionSnapshot(), Modifier.weight(1f))
        } else {
            Text(page.title, Modifier.weight(1f), fontSize = 24.sp, fontWeight = FontWeight.Medium,
                maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable private fun ServerMessageHeader(session: SessionSnapshot, modifier: Modifier) {
    val active = session.phase == Phase.CONNECTING || session.phase == Phase.CONNECTED
    val message = remember(session.transport, active) {
        if (active) TransportDiagnostics.parse(session.transport).root.field("ServerMessage").orEmpty() else ""
    }
    InfoCard(modifier, contentPadding = 8.dp) {
        Text("Server message", color = Muted, style = MaterialTheme.typography.labelSmall)
        val formatted = remember(message) { serverMessageText(message) }
        Column(Modifier.fillMaxWidth().heightIn(max = 40.dp).verticalScroll(rememberScrollState())) {
            if (formatted.text.isBlank()) {
                Text(if (active) "No message from this server." else "Connect to view the message.",
                    style = MaterialTheme.typography.bodySmall, color = Muted)
            } else {
                Text(formatted, style = MaterialTheme.typography.bodySmall, color = Ink)
            }
        }
    }
}

@Composable fun ItomBottomBar(page: Page, navigate: (Page) -> Unit) {
    Row(Modifier.windowInsetsPadding(WindowInsets.safeDrawing.only(WindowInsetsSides.Bottom + WindowInsetsSides.Horizontal))
        .padding(horizontal = 16.dp, vertical = 8.dp).fillMaxWidth().clip(RoundedCornerShape(26.dp))
        .background(Brush.verticalGradient(listOf(Color(0xFF111B2B), Color(0xFF0A111C))))
        .border(1.dp, Outline.copy(alpha = .8f), RoundedCornerShape(26.dp)).padding(5.dp), horizontalArrangement = Arrangement.spacedBy(4.dp)) {
        listOf(Page.HOME to Glyph.HOME, Page.PROFILES to Glyph.SERVERS, Page.RULES to Glyph.ROUTES, Page.SETTINGS to Glyph.SETTINGS).forEach { (tab, glyph) ->
            val selected = page.root == tab.root
            val color by animateColorAsState(if (selected) Blue else Muted, label = "navigationColor")
            Column(Modifier.weight(1f).clip(RoundedCornerShape(21.dp))
                .background(Brush.linearGradient(if (selected) listOf(Color(0xFF123A52), Color(0xFF102537)) else listOf(Color.Transparent, Color.Transparent)))
                .border(1.dp, if (selected) Blue.copy(alpha = .45f) else Color.Transparent, RoundedCornerShape(21.dp))
                .selectable(selected, role = Role.Tab, onClick = { navigate(tab) }).padding(vertical = 8.dp),
                horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.spacedBy(4.dp)) {
                Box(contentAlignment = Alignment.Center) {
                    if (selected) Canvas(Modifier.size(26.dp)) { drawCircle(Brush.radialGradient(listOf(Blue.copy(alpha = .2f), Color.Transparent))) }
                    AppIcon(glyph, Modifier.size(23.dp), color)
                }
                Text(if (tab == Page.HOME) "Home" else tab.title, fontSize = 10.sp, lineHeight = 13.sp, color = color, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
        }
    }
}

@Composable fun StatusLabel(text: String, color: Color) {
    Text(text, color = color, style = MaterialTheme.typography.labelSmall,
        modifier = Modifier.clip(RoundedCornerShape(8.dp)).background(color.copy(alpha = .10f)).padding(horizontal = 8.dp, vertical = 5.dp))
}

@Composable fun DiagnosticCode(code: String, terminal: Boolean = false) {
    val level = if (terminal) org.itom.domain.DiagnosticLevel.ERROR else org.itom.domain.diagnosticLevel(code)
    val color = when (level) { org.itom.domain.DiagnosticLevel.INFO -> Blue; org.itom.domain.DiagnosticLevel.WARNING -> Yellow; org.itom.domain.DiagnosticLevel.ERROR -> Red }
    val label = when (level) { org.itom.domain.DiagnosticLevel.INFO -> "Information"; org.itom.domain.DiagnosticLevel.WARNING -> "Warning"; org.itom.domain.DiagnosticLevel.ERROR -> "Error" }
    StatusLabel("$label · $code", color)
}
