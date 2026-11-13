package android

// killedTBEnv makes the test binary a tb that starts a recording and waits to be killed
// (TestKilledTBHelper, recording_test.go, issue #52); its value is the recording's path.
// TestMain makes no stubs then: the test's own come in its environment. Not limited to Unix
// like those tests: TestMain refers to it on every platform.
const killedTBEnv = "TB_TEST_ANDROID_KILLED_TB"
