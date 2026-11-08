import Network
import SafariServices
import SwiftUI

struct LocalBrowser: UIViewControllerRepresentable {
    func makeUIViewController(context: Context) -> BrowserHost { BrowserHost() }
    func updateUIViewController(_ host: BrowserHost, context: Context) {}
}

final class BrowserHost: UIViewController {
    private var listener: NWListener?

    override func viewDidLoad() {
        super.viewDidLoad()
        let button = UIButton(type: .system)
        button.setTitle("Open local sign-in", for: .normal)
        button.accessibilityIdentifier = "tk_open_browser"
        button.addTarget(self, action: #selector(openBrowser), for: .touchUpInside)
        button.translatesAutoresizingMaskIntoConstraints = false
        view.addSubview(button)
        NSLayoutConstraint.activate([
            button.centerXAnchor.constraint(equalTo: view.centerXAnchor),
            button.topAnchor.constraint(equalTo: view.safeAreaLayoutGuide.topAnchor, constant: 16),
        ])
    }

    @objc private func openBrowser() {
        if let port = listener?.port {
            showPage(port)
            return
        }
        let config = NWParameters.tcp
        config.requiredLocalEndpoint = .hostPort(host: "127.0.0.1", port: .any)
        guard let listener = try? NWListener(using: config) else { return }
        self.listener = listener
        listener.newConnectionHandler = Self.serve
        listener.stateUpdateHandler = { [weak self, weak listener] state in
            if case .ready = state, let port = listener?.port { self?.showPage(port) }
        }
        listener.start(queue: .main)
    }

    private func showPage(_ port: NWEndpoint.Port) {
        guard let url = URL(string: "http://127.0.0.1:\(port.rawValue)/") else { return }
        let browser = SFSafariViewController(url: url)
        browser.modalPresentationStyle = .fullScreen
        present(browser, animated: true)
    }

    private static func serve(_ connection: NWConnection) {
        connection.start(queue: .main)
        connection.receive(minimumIncompleteLength: 1, maximumLength: 8192) { _, _, _, _ in
            let html = """
            <!doctype html><meta name="viewport" content="width=device-width,initial-scale=1">
            <title>Local sign-in</title><h1>Account login</h1>
            <label>Email <input type="email"></label>
            <button>Sign in to account</button><p><a href="#reset">Reset password</a></p>
            """
            let headers = "HTTP/1.1 200 OK\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: \(html.utf8.count)\r\nConnection: close\r\n\r\n"
            connection.send(content: Data((headers + html).utf8), completion: .contentProcessed { _ in
                connection.cancel()
            })
        }
    }

    deinit { listener?.cancel() }
}
