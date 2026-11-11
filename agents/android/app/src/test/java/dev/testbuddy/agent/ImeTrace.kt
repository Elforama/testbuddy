package dev.testbuddy.agent

import java.io.ByteArrayOutputStream

/** `dumpsys input_method --proto` outputs: one captured on a device, others built here. */
object ImeTrace {
    /**
     * API 36 emulator, the single-line Compose field of the bench's Compose toolkit screen
     * focused: its EditorInfo is inputType=0x8001 imeOptions=0x2000006 (Done), fieldId=-1.
     */
    val singleLineCompose: ByteArray = hex(
        "09494d4d545241434519ac6f4c83f857e61812fd0409d507697c85c40000123c496e7075744d6574686f644d616e61676572" +
            "536572766963652e6d5072696f7269747944756d7065722364756d70417350726f746f4e6f436865636b1ab3040a4b636f6d" +
            "2e676f6f676c652e616e64726f69642e696e7075746d6574686f642e6c6174696e2f636f6d2e616e64726f69642e696e7075" +
            "746d6574686f642e6c6174696e2e4c6174696e494d45108b171a43436c69656e7453746174657b37363261623234206d5569" +
            "643d3130323134206d5069643d3230313339206d53656c665265706f72746564446973706c617949643d307d224634303264" +
            "623638206465762e7465737462756464792e62656e63682f6465762e7465737462756464792e62656e63682e546f6f6c6b69" +
            "74436f6d706f73654163746976697479323253544154455f554e5350454349464945447c41444a5553545f50414e7c49535f" +
            "464f52574152445f4e415649474154494f4e2a4634303264623638206465762e7465737462756464792e62656e63682f6465" +
            "762e7465737462756464792e62656e63682e546f6f6c6b6974436f6d706f73654163746976697479323253544154455f554e" +
            "5350454349464945447c41444a5553545f50414e7c49535f464f52574152445f4e415649474154494f4e3a29088180021086" +
            "80801022136465762e7465737462756464792e62656e636828ffffffffffffffffff01424b636f6d2e676f6f676c652e616e" +
            "64726f69642e696e7075746d6574686f642e6c6174696e2f636f6d2e616e64726f69642e696e7075746d6574686f642e6c61" +
            "74696e2e4c6174696e494d45500160017219616e64726f69642e6f732e42696e646572403861646536383080010190010198" +
            "0101a00101b00103",
    )

    /**
     * A trace whose current editor has these settings; [editor] false leaves the editor out
     * (none focused). Same layout as the device's: magic (field 1), an entry (2) holding the
     * service's state (3), whose field 7 is the current EditorInfo.
     */
    fun of(inputType: Int, imeOptions: Int, pkg: String?, editor: Boolean = true): ByteArray {
        val info = ByteArrayOutputStream().apply {
            if (inputType != 0) varint(1, inputType.toLong())
            if (imeOptions != 0) varint(2, imeOptions.toLong())
            if (pkg != null) bytes(4, pkg.toByteArray())
            varint(5, -1)
        }.toByteArray()
        val service = ByteArrayOutputStream().apply {
            bytes(1, "com.example.ime/.Ime".toByteArray())
            varint(2, 42)
            if (editor) bytes(7, info)
            bytes(8, "id".toByteArray())
        }.toByteArray()
        val entry = ByteArrayOutputStream().apply {
            fixed64(1, ByteArray(8))
            bytes(2, "InputMethodManagerService.mPriorityDumper#dumpAsProtoNoCheck".toByteArray())
            bytes(3, service)
        }.toByteArray()
        return ByteArrayOutputStream().apply {
            fixed64(1, "IMMTRACE".toByteArray())
            fixed64(3, ByteArray(8))
            bytes(2, entry)
        }.toByteArray()
    }

    private fun ByteArrayOutputStream.raw(v: Long) {
        var x = v
        while (x and 0x7fL.inv() != 0L) { write(((x and 0x7f) or 0x80).toInt()); x = x ushr 7 }
        write(x.toInt())
    }
    private fun ByteArrayOutputStream.varint(field: Int, v: Long) { raw((field shl 3).toLong()); raw(v) }
    private fun ByteArrayOutputStream.bytes(field: Int, b: ByteArray) { raw((field shl 3 or 2).toLong()); raw(b.size.toLong()); write(b) }
    private fun ByteArrayOutputStream.fixed64(field: Int, b: ByteArray) { raw((field shl 3 or 1).toLong()); write(b) }

    private fun hex(s: String) = ByteArray(s.length / 2) { s.substring(it * 2, it * 2 + 2).toInt(16).toByte() }
}
