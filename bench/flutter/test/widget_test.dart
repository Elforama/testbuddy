import 'package:buddy_bench_flutter/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets('Journey opens from the scenario menu', (tester) async {
    await tester.pumpWidget(const MaterialApp(home: Menu()));
    await tester.tap(find.text('Journey'));
    await tester.pump();
    await tester.pump(const Duration(milliseconds: 400));
    expect(find.text('Buddy Bench'), findsOneWidget);
  });
}
