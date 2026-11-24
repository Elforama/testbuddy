import Foundation

struct QuietArm: Equatable {
    let gen: Int64
    let solid: Int64

    func settles(gen now: Int64, solid: Int64) -> Bool {
        gen == now && solid == self.solid
    }

    static func next(after previous: QuietArm?, solid: Int64) -> QuietArm {
        QuietArm(gen: (previous?.gen ?? 0) + 1, solid: max(previous?.solid ?? solid, solid))
    }
}
