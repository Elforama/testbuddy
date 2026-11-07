import Flutter
import UIKit

class SceneDelegate: FlutterSceneDelegate {
    override func scene(_ scene: UIScene, willConnectTo session: UISceneSession,
                        options connectionOptions: UIScene.ConnectionOptions) {
        super.scene(scene, willConnectTo: session, options: connectionOptions)
        openLevel(in: scene, contexts: connectionOptions.urlContexts)
    }

    override func scene(_ scene: UIScene, openURLContexts contexts: Set<UIOpenURLContext>) {
        if !openLevel(in: scene, contexts: contexts) {
            super.scene(scene, openURLContexts: contexts)
        }
    }

    @discardableResult
    private func openLevel(in scene: UIScene, contexts: Set<UIOpenURLContext>) -> Bool {
        guard let url = contexts.map(\.url).first(where: { $0.scheme == "benchflutter" && $0.host == "level" }),
              let depth = Int(url.lastPathComponent),
              let flutter = (scene as? UIWindowScene)?.windows.first(where: \.isKeyWindow)?.rootViewController as? FlutterViewController else {
            return false
        }
        flutter.engine.navigationChannel.invokeMethod("pushRoute", arguments: "/level/\(min(5, max(0, depth)))")
        return true
    }
}
