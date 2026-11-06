import 'package:everyday_bench/main.dart';
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';

void main() {
  testWidgets(
    'Fake login accepts blank credentials and items survive tab changes',
    (tester) async {
      await tester.pumpWidget(const EverydayApp());
      await tester.tap(find.text('Sign in'));
      await tester.pump();
      expect(find.text('Signing in…'), findsOneWidget);
      await tester.pump(const Duration(milliseconds: 500));
      await tester.pumpAndSettle();
      expect(find.text('My list'), findsWidgets);
      await tester.tap(find.text('Add item'));
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), 'Coffee');
      await tester.tap(find.text('OK'));
      await tester.pumpAndSettle();
      expect(find.text('Coffee'), findsOneWidget);
      await tester.tap(find.text('Settings'));
      await tester.pumpAndSettle();
      expect(find.text('Preferences'), findsOneWidget);
      await tester.tap(find.text('My list'));
      await tester.pumpAndSettle();
      expect(find.text('Coffee'), findsOneWidget);
      await tester.tap(find.text('Add item'));
      await tester.pumpAndSettle();
      await tester.enterText(find.byType(TextField), 'Cancelled');
      await tester.tap(find.text('Cancel'));
      await tester.pumpAndSettle();
      expect(find.text('Cancelled'), findsNothing);
      expect(find.text('4 items'), findsOneWidget);
    },
  );
}
