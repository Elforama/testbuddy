// When the UI counts as settled (Events): after each solid change the quiet timer is armed;
// when it fires the app is asked whether it is idle, and its answer settles the UI unless the
// UI changed meanwhile.
import Foundation

/// One arming of the quiet timer: the timer generation it armed and the solid change count it
/// saw then.
struct QuietArm: Equatable {
    let gen: Int64
    let solid: Int64

    /// Whether the app's idle answer to this arming settles the UI, given the generation and
    /// the solid change count when the answer is handled. A later arming is not enough to go
    /// by: a change counts at once (Events.change, under its lock) but re-arms the timer only
    /// later, from the events queue, and an answer handled in between would settle the UI with
    /// that change in it. A popover's first commit, as the app's main thread came out of a
    /// hang, did that: the host's wait for a still screen returned at once, and its tap landed
    /// as the popover began to present and was dropped (issue #170). The change's own timer
    /// settles it.
    func settles(gen now: Int64, solid: Int64) -> Bool {
        gen == now && solid == self.solid
    }
}
