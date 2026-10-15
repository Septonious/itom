package org.itom.client

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicText
import androidx.compose.foundation.text.TextAutoSize
import androidx.compose.material3.*
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import org.itom.domain.*

val Blue = Color(0xFF25C9FF)
val ActionBlue = Color(0xFF25C9FF)
val Green = Color(0xFF37E5B0)
val Yellow = Color(0xFFFFCF67)
val Red = Color(0xFFFF6685)
val Ink = Color(0xFFF3F7FF)
val Muted = Color(0xFFB0BED2)
val Panel = Color(0xFF101827)
val Background = Color(0xFF060A12)
val OnAccent = Color(0xFF04121F)
val Violet = Color(0xFFB893FF)
val Outline = Color(0xFF5A708E)

@Composable fun ItomSwitch(checked: Boolean, onCheckedChange: (Boolean) -> Unit, modifier: Modifier = Modifier, enabled: Boolean = true) {
    Switch(checked = checked, onCheckedChange = onCheckedChange, modifier = modifier, enabled = enabled,
        colors = SwitchDefaults.colors(
            checkedThumbColor = Color(0xFF05291D), checkedTrackColor = Green, checkedBorderColor = Green,
            uncheckedThumbColor = Muted, uncheckedTrackColor = Color(0xFF182337), uncheckedBorderColor = Outline,
            disabledCheckedThumbColor = Color(0xFF91C2AC), disabledCheckedTrackColor = Color(0xFF244C3D),
            disabledUncheckedThumbColor = Muted.copy(alpha = .4f), disabledUncheckedTrackColor = Color(0xFF101827)))
}

fun phaseColor(p: Phase) = when (p) { Phase.OFF -> Color(0xFF8193A4); Phase.CONNECTING -> Yellow; Phase.CONNECTED -> Green; Phase.ERROR -> Red }
fun phaseName(p: Phase) = when (p) { Phase.OFF -> "Disconnected"; Phase.CONNECTING -> "Connecting"; Phase.CONNECTED -> "Connected"; Phase.ERROR -> "Disconnected due to an error" }
fun routeName(action: RouteAction) = when (action) { RouteAction.VPN -> "VPN"; RouteAction.BLOCK -> "Block"; RouteAction.DIRECT -> "Direct" }
fun routeColor(action: RouteAction) = when (action) { RouteAction.VPN -> Blue; RouteAction.BLOCK -> Red; RouteAction.DIRECT -> Green }
fun rttColor(ms: Int?, t: RttThresholds) = when (Presentation.rttBand(ms, t)) { 1 -> Green; 2 -> Yellow; 3 -> Red; else -> Muted }
fun appModeName(mode: AppMode) = when (mode) { AppMode.ALL -> "All applications use VPN"; AppMode.EXCLUDE_SELECTED -> "Selected applications bypass VPN"; AppMode.INCLUDE_SELECTED -> "Only selected applications use VPN" }

@Composable fun ItomTheme(content: @Composable () -> Unit) {
    // Keep Android's font scale; the removed in-app multiplier is no longer applied.
    MaterialTheme(
        colorScheme = darkColorScheme(primary = Blue, onPrimary = OnAccent, secondary = Green, onSecondary = OnAccent, tertiary = Violet, onTertiary = OnAccent,
            background = Background, surface = Panel, onBackground = Ink, onSurface = Ink,
            surfaceVariant = Color(0xFF182337), onSurfaceVariant = Muted, outline = Outline,
            outlineVariant = Outline.copy(alpha = .6f), error = Red, onError = OnAccent,
            primaryContainer = Color(0xFF10324C), onPrimaryContainer = Ink,
            secondaryContainer = Color(0xFF153D35), onSecondaryContainer = Ink,
            tertiaryContainer = Color(0xFF153D35), onTertiaryContainer = Ink,
            inverseSurface = Color(0xFF1B293E), inverseOnSurface = Ink, inversePrimary = Blue),
        shapes = Shapes(small = RoundedCornerShape(12.dp), medium = RoundedCornerShape(18.dp), large = RoundedCornerShape(24.dp)),
        typography = Typography(
            headlineSmall = TextStyle(fontSize = 24.sp, lineHeight = 30.sp, fontWeight = FontWeight.Medium),
            titleLarge = TextStyle(fontSize = 22.sp, lineHeight = 28.sp, fontWeight = FontWeight.Medium),
            titleMedium = TextStyle(fontSize = 16.sp, lineHeight = 22.sp, fontWeight = FontWeight.Medium),
            bodyLarge = TextStyle(fontSize = 15.sp, lineHeight = 22.sp),
            bodyMedium = TextStyle(fontSize = 14.sp, lineHeight = 20.sp),
            bodySmall = TextStyle(fontSize = 12.sp, lineHeight = 17.sp),
            labelLarge = TextStyle(fontSize = 14.sp, lineHeight = 19.sp, fontWeight = FontWeight.Medium),
            labelMedium = TextStyle(fontSize = 12.sp, lineHeight = 16.sp, fontWeight = FontWeight.Medium),
            labelSmall = TextStyle(fontSize = 11.sp, lineHeight = 14.sp, fontWeight = FontWeight.Medium)
        ), content = {
            // Gradient panels are Columns, not Surfaces: establish their inherited text color explicitly.
            CompositionLocalProvider(LocalContentColor provides Ink, content = content)
        }
    )
}

@Composable fun ItomBackdrop(content: @Composable BoxScope.() -> Unit) {
    Box(Modifier.fillMaxSize().background(Background).drawBehind {
        drawRect(Brush.verticalGradient(listOf(Color(0xFF0B1320), Background)))
        val radius = size.maxDimension.coerceAtLeast(1f) * .7f
        drawCircle(Brush.radialGradient(listOf(Blue.copy(alpha = .045f), Color.Transparent), Offset(size.width, 0f), radius), radius, Offset(size.width, 0f))
    }, content = content)
}

@Composable fun PageColumn(content: @Composable ColumnScope.() -> Unit) {
    Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp), verticalArrangement = Arrangement.spacedBy(14.dp), content = content)
}

@Composable fun InfoCard(
    modifier: Modifier = Modifier, accent: Color? = null, contentPadding: Dp = 16.dp,
    onClick: (() -> Unit)? = null, content: @Composable ColumnScope.() -> Unit
) {
    val shape = RoundedCornerShape(22.dp)
    Column(modifier.clip(shape)
        .background(Brush.linearGradient(listOf(accent?.copy(alpha = .12f) ?: Color(0xFF131E30), Color(0xFF0D1421))))
        .border(1.dp, Brush.linearGradient(listOf(accent?.copy(alpha = .48f) ?: Color(0xFF3B4D67), Outline.copy(alpha = .55f))), shape)
        .then(if (onClick != null) Modifier.clickable(onClick = onClick) else Modifier)
        .padding(contentPadding), verticalArrangement = Arrangement.spacedBy(8.dp), content = content)
}

@Composable fun SmallNote(text: String, modifier: Modifier = Modifier, color: Color = Muted) {
    Text(text, modifier = modifier, color = color, style = MaterialTheme.typography.bodySmall)
}

/** Bounded, single-line labels adapt locally without changing the system font scale. */
@Composable fun FitText(text: String, modifier: Modifier = Modifier, color: Color = Ink, maxSize: Int = 28, minSize: Int = 14, align: TextAlign = TextAlign.Start) {
    BasicText(text, modifier, style = MaterialTheme.typography.bodyLarge.copy(color = color, fontWeight = FontWeight.Medium,
        fontFeatureSettings = "tnum", textAlign = align, lineHeight = TextUnit.Unspecified), maxLines = 1, overflow = TextOverflow.Ellipsis,
        autoSize = TextAutoSize.StepBased(minFontSize = minSize.sp, maxFontSize = maxSize.sp, stepSize = 1.sp))
}
