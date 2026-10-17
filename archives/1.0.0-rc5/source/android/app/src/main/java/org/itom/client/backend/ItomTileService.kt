package org.itom.client.backend

import android.app.PendingIntent
import android.content.Intent
import android.service.quicksettings.Tile
import android.service.quicksettings.TileService
import androidx.core.service.quicksettings.PendingIntentActivityWrapper
import androidx.core.service.quicksettings.TileServiceCompat
import org.itom.client.MainActivity

/** Opens the visible UI; no implicit reconnect or background consent request. */
class ItomTileService : TileService() {
    override fun onStartListening() {
        qsTile?.apply { state = Tile.STATE_INACTIVE; label = "ITOM"; subtitle = "Open"; updateTile() }
    }
    override fun onClick() {
        val intent = Intent(this, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        // AndroidX uses PendingIntent on API 34+ and the compatible API on older devices.
        // The final false requests an immutable PendingIntent with an explicit activity target.
        val launch = PendingIntentActivityWrapper(this, 0, intent, PendingIntent.FLAG_UPDATE_CURRENT, false)
        TileServiceCompat.startActivityAndCollapse(this, launch)
    }
}
