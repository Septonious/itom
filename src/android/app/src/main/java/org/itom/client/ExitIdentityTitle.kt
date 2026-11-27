package org.itom.client

import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.*
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import android.graphics.BitmapFactory
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.itom.domain.ExitIdentity

@Composable internal fun ExitIdentityTitle(identity: ExitIdentity?) {
    val context = LocalContext.current
    val code = identity?.country.orEmpty()
    val bitmap by produceState<ImageBitmap?>(null, code) {
        value = null
        if (code.matches(Regex("[A-Z]{2}"))) value = withContext(Dispatchers.IO) {
            runCatching { context.assets.open("flags/${code.lowercase(java.util.Locale.ROOT)}.png").use { BitmapFactory.decodeStream(it)?.asImageBitmap() } }.getOrNull()
        }
    }
    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
        bitmap?.let { Image(it, contentDescription = code, modifier = Modifier.size(24.dp, 16.dp)) }
        Text(identity?.ip ?: "Exit IP unavailable", style = MaterialTheme.typography.titleMedium, maxLines = 1, overflow = TextOverflow.Ellipsis)
    }
}
