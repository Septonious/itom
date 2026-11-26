package org.itom.client

import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withStyle
import org.itom.domain.ServerMessageFormatting

internal fun serverMessageText(raw: String): AnnotatedString = buildAnnotatedString {
    for (run in ServerMessageFormatting.parse(raw)) {
        val style = run.style
        val decorations = buildList {
            if (style.underline) add(TextDecoration.Underline)
            if (style.strikethrough) add(TextDecoration.LineThrough)
        }
        withStyle(SpanStyle(
            color = style.colorRgb?.let { Color(0xFF000000.toInt() or it) } ?: Color.Unspecified,
            fontWeight = if (style.bold) FontWeight.Bold else null,
            fontStyle = if (style.italic) FontStyle.Italic else null,
            textDecoration = if (decorations.isEmpty()) TextDecoration.None else TextDecoration.combine(decorations),
        )) { append(run.text) }
    }
}
