plugins {
    id("com.android.application")
}

android {
    namespace = "dev.testbuddy.agent"
    compileSdk = 36

    defaultConfig {
        applicationId = "dev.testbuddy.agent"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "0.1"
    }

    buildTypes {
        debug { isMinifyEnabled = false }
    }

    // Unit tests run on the JVM against stubs of Android's classes: a call into one returns
    // a default value (0, null) instead of throwing, so code that only reads the clock runs.
    testOptions {
        unitTests.isReturnDefaultValues = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    // Unit tests of the agent's plain-Kotlin parts (./gradlew testDebugUnitTest: CI's Android
    // agent job). The agent itself has no dependencies.
    testImplementation("junit:junit:4.13.2")
}
