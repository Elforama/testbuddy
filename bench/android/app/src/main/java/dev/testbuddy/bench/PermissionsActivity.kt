package dev.testbuddy.bench

import android.Manifest
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.widget.TextView
import androidx.activity.result.ActivityResultLauncher
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat

/** Scenario 3, "Permissions": real runtime permission dialogs from the system. */
class PermissionsActivity : ScenarioActivity() {
    private class Perm(val name: String, val permission: String?, val statusId: Int) {
        lateinit var status: TextView
        lateinit var launcher: ActivityResultLauncher<String>
    }

    private val notifications = if (Build.VERSION.SDK_INT >= 33) Manifest.permission.POST_NOTIFICATIONS else null
    private val perms = listOf(
        Perm("Camera", Manifest.permission.CAMERA, R.id.perm_camera_status),
        Perm("Location", Manifest.permission.ACCESS_FINE_LOCATION, R.id.perm_location_status),
        Perm("Notifications", notifications, R.id.perm_notifications_status),
        Perm("Contacts", Manifest.permission.READ_CONTACTS, R.id.perm_contacts_status),
        Perm("Microphone", Manifest.permission.RECORD_AUDIO, R.id.perm_mic_status),
    )

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        screen(Scenario.PERMISSIONS.title, scroll = true)
        for (p in perms) {
            p.launcher = registerForActivityResult(ActivityResultContracts.RequestPermission()) { ok ->
                AppState.askedPermissions += p.name
                p.status.text = "${p.name}: ${if (ok) "granted" else "denied"}"
            }
        }
        val (camera, location, notify, contacts, mic) = perms
        askRow(R.id.perm_camera, "Ask camera", camera)
        askRow(R.id.perm_location, "Ask location", location)
        askRow(R.id.perm_notifications, "Ask notifications", notify)
        askRow(R.id.perm_contacts, "Ask contacts", contacts)

        content.heading("Status")
        content.button(R.id.perm_refresh, "Refresh") { perms.forEach(::readStatus) }

        content.heading("In-app rationale")
        content.button(R.id.perm_rationale, "Ask with rationale") {
            inAppDialog(R.id.rationale_dialog, "We need your microphone",
                Triple(R.id.rationale_no, "Not now") {},
                Triple(R.id.rationale_yes, "Continue") { ask(mic) },
            ).show()
        }
        mic.status = content.text(mic.statusId)

        content.heading("Notification")
        val postStatus = content.text(R.id.perm_post_status, "Not posted")
        content.button(R.id.perm_post, "Post a notification") {
            postStatus.text = if (postNotification()) "Posted" else "Not allowed"
        }
        perms.forEach(::readStatus)
    }

    private fun askRow(buttonId: Int, label: String, p: Perm) {
        content.heading(p.name)
        content.button(buttonId, label) { ask(p) }
        p.status = content.text(p.statusId)
    }

    private fun ask(p: Perm) {
        val permission = p.permission
        if (permission == null) { // notifications before Android 13: no runtime permission
            p.status.text = "${p.name}: granted"
            return
        }
        p.launcher.launch(permission)
    }

    /** From the system, no prompt: granted, else denied once asked (this process), else not asked. */
    private fun readStatus(p: Perm) {
        val permission = p.permission
        val granted = permission == null ||
            ContextCompat.checkSelfPermission(this, permission) == PackageManager.PERMISSION_GRANTED
        p.status.text = when {
            permission == null && p.name in AppState.askedPermissions -> "${p.name}: granted"
            permission == null -> "${p.name}: not asked"
            granted -> "${p.name}: granted"
            p.name in AppState.askedPermissions || shouldShowRequestPermissionRationale(permission) -> "${p.name}: denied"
            else -> "${p.name}: not asked"
        }
    }

    private fun postNotification(): Boolean {
        val nm = NotificationManagerCompat.from(this)
        if (!nm.areNotificationsEnabled()) return false
        if (Build.VERSION.SDK_INT >= 33 && ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS)
            != PackageManager.PERMISSION_GRANTED) return false
        getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CHANNEL, "Bench", NotificationManager.IMPORTANCE_DEFAULT))
        val n = NotificationCompat.Builder(this, CHANNEL)
            .setSmallIcon(android.R.drawable.ic_dialog_info)
            .setContentTitle("Bench says hi")
            .setAutoCancel(true)
            .build()
        return try {
            nm.notify(1, n)
            true
        } catch (_: SecurityException) {
            false
        }
    }

    companion object {
        const val CHANNEL = "bench"
    }
}
