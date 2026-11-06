part of 'main.dart';

class CollectionsScreen extends StatelessWidget {
  const CollectionsScreen({super.key});
  static const pages = {
    'long_list': 'Long list',
    'grid': 'Grid',
    'carousels': 'Carousels',
    'two_pane': 'Two panes',
    'refresh': 'Pull to refresh',
    'swipe_delete': 'Swipe to delete',
    'reorder': 'Reorder',
    'infinite': 'Infinite feed',
    'empty': 'Empty list',
  };
  @override
  Widget build(BuildContext c) => frame(
    c,
    scenarios['lists']!,
    page([
      for (final e in pages.entries)
        button(
          'open_${e.key}',
          e.value,
          () =>
              push(c, e.key == 'infinite' ? const Feed() : ListExample(e.key)),
        ),
    ]),
  );
}

class ListExample extends StatefulWidget {
  const ListExample(this.kind, {super.key});
  final String kind;
  @override
  State<ListExample> createState() => _ListExampleState();
}

class _ListExampleState extends State<ListExample> {
  String picked = '';
  int mail = 1, count = 0;
  final order = ['A', 'B', 'C', 'D', 'E'];
  final tasks = List.generate(8, (i) => i + 1);
  Widget row(String name) => SizedBox(
    height: 56,
    child: button(
      name.toLowerCase().replaceAll(' ', '_'),
      name,
      () => setState(() => picked = 'Picked $name'),
    ),
  );
  @override
  Widget build(BuildContext c) {
    final Widget body = switch (widget.kind) {
      'long_list' => CustomScrollView(
        slivers: [
          for (int section = 0; section < 20; section++)
            SliverMainAxisGroup(
              slivers: [
                SliverAppBar(
                  primary: false,
                  automaticallyImplyLeading: false,
                  pinned: true,
                  toolbarHeight: 30,
                  title: Text('Section ${String.fromCharCode(65 + section)}'),
                ),
                SliverList.builder(
                  itemCount: 25,
                  itemBuilder: (_, i) => row('Row ${section * 25 + i + 1}'),
                ),
              ],
            ),
        ],
      ),
      'grid' => GridView.count(
        crossAxisCount: 3,
        children: [for (int i = 1; i <= 120; i++) row('Cell $i')],
      ),
      'carousels' => ListView(
        children: [
          for (int s = 1; s <= 10; s++)
            Column(
              children: [
                Text('Shelf $s'),
                SizedBox(
                  height: 85,
                  child: ListView(
                    scrollDirection: Axis.horizontal,
                    children: [
                      for (int i = 1; i <= 15; i++)
                        SizedBox(width: 140, child: row('Card $s-$i')),
                    ],
                  ),
                ),
              ],
            ),
        ],
      ),
      'two_pane' => Row(
        children: [
          Expanded(
            child: ListView.builder(
              itemCount: 80,
              itemBuilder: (_, i) => button(
                'mail_${i + 1}',
                'Mail ${i + 1}',
                () => setState(() => mail = i + 1),
              ),
            ),
          ),
          Expanded(
            child: Column(
              children: [
                label('mail_detail', 'Mail $mail'),
                Expanded(
                  child: ListView.builder(
                    key: ValueKey(mail),
                    itemCount: 60,
                    itemBuilder: (_, i) => SizedBox(
                      height: 42,
                      child: Text('Line ${i + 1} of mail $mail'),
                    ),
                  ),
                ),
              ],
            ),
          ),
        ],
      ),
      'refresh' => RefreshIndicator(
        onRefresh: () async {
          await Future<void>.delayed(const Duration(milliseconds: 1000));
          if (mounted) setState(() => count++);
        },
        child: ListView(
          physics: const AlwaysScrollableScrollPhysics(),
          children: [
            Text('Refreshed $count times'),
            for (int i = 1; i <= 20; i++) row('Row $i'),
          ],
        ),
      ),
      'reorder' => Column(
        children: [
          label('order_status', 'Order: ${order.join()}'),
          Expanded(
            child: ReorderableListView(
              onReorder: (a, b) => setState(() {
                order.insert(b > a ? b - 1 : b, order.removeAt(a));
              }),
              children: [
                for (final e in order)
                  ListTile(
                    key: ValueKey(e),
                    title: label('reorder_$e', e),
                    trailing: mark('handle_$e', const Icon(Icons.drag_handle)),
                  ),
              ],
            ),
          ),
        ],
      ),
      'swipe_delete' => ListView(
        children: [
          Text('Tasks: ${tasks.length}'),
          for (final t in tasks)
            Dismissible(
              key: ValueKey(t),
              direction: DismissDirection.endToStart,
              onDismissed: (_) => setState(() => tasks.remove(t)),
              child: mark('task_$t', ListTile(title: Text('Task $t'))),
            ),
        ],
      ),
      _ => page([
        if (count == 0) const Text('Nothing here yet'),
        for (int i = 1; i <= count; i++) Text('New row $i'),
        button('empty_add', 'Add', () => setState(() => count++)),
      ]),
    };
    return frame(
      c,
      CollectionsScreen.pages[widget.kind]!,
      Column(
        children: [
          label('picked', picked),
          Expanded(child: body),
        ],
      ),
    );
  }
}

class Feed extends StatefulWidget {
  const Feed({super.key, this.slow = false});
  final bool slow;
  @override
  State<Feed> createState() => _FeedState();
}

class _FeedState extends State<Feed> {
  final scroll = ScrollController();
  int count = 20;
  bool loading = false;
  @override
  void initState() {
    super.initState();
    scroll.addListener(load);
    if (widget.slow) {
      count = 0;
      loadPage();
    }
  }

  Future<void> load() async {
    if (loading ||
        scroll.position.extentAfter > 150 ||
        count >= (widget.slow ? 40 : 200)) {
      return;
    }
    await loadPage();
  }

  Future<void> loadPage() async {
    setState(() => loading = true);
    await Future<void>.delayed(
      Duration(milliseconds: widget.slow ? 2500 : 800),
    );
    if (mounted) {
      setState(() {
        count += 20;
        loading = false;
      });
    }
  }

  @override
  void dispose() {
    scroll.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext c) => frame(
    c,
    widget.slow ? 'Slow list' : 'Infinite feed',
    ListView.builder(
      controller: scroll,
      itemCount: count + (loading ? 1 : 0),
      itemBuilder: (_, i) => i == count
          ? const LinearProgressIndicator()
          : SizedBox(
              height: 46,
              child: Text('${widget.slow ? 'Slow item' : 'Post'} ${i + 1}'),
            ),
    ),
  );
}
