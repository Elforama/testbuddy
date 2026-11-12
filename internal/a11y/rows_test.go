package a11y

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// fixedAndLeft is "file added" for each fix and "file: reason" for each finding left.
func fixedAndLeft(rep *FixReport) (fixed []string, left map[string][]string) {
	left = map[string][]string{}
	for _, f := range rep.Fixed {
		fixed = append(fixed, filepath.Base(f.File)+" "+f.Added)
	}
	sort.Strings(fixed)
	for _, l := range rep.Left {
		left[filepath.Base(l.File)] = append(left[filepath.Base(l.File)], l.Reason)
	}
	return fixed, left
}

func expectFixed(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("fixed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// expectLeftFor: file has one finding left, whose reason says it needs a per-item id and
// holds each of says.
func expectLeftFor(t *testing.T, left map[string][]string, file string, says ...string) {
	t.Helper()
	rs := left[file]
	if len(rs) != 1 {
		t.Errorf("%s: left %d %q, want 1", file, len(rs), rs)
		return
	}
	for _, s := range append([]string{"per-item id"}, says...) {
		if !strings.Contains(rs[0], s) {
			t.Errorf("%s: reason %q does not say %q", file, rs[0], s)
		}
	}
}

// Issue #147: --fix gave one fixed id to an element whose loop is elsewhere: a row
// component a .map() in another file repeats (and what that row renders in turn), a row
// defined apart in its own file and passed as renderItem={renderRow} (or a class's
// this.renderRow), a web or Vue component a loop in another file repeats. They are left for a person, like a loop's own items; what
// renders once (the screen's own buttons, a component no loop renders) is still fixed.
func TestFixLeavesRowsRenderedElsewhere(t *testing.T) {
	files := map[string]string{
		"src/List.tsx": `import { FlatList, Pressable, View } from 'react-native';
import { Row } from './Row';
export function L({ items }) {
  const renderRow = ({ item }) => (
    <Pressable onPress={() => open(item)}><Icon /></Pressable>
  );
  return (
    <View>
      <FlatList data={items} renderItem={renderRow} />
      {items.map(i => <Row key={i.id} item={i} />)}
      <Pressable onPress={refresh}><Icon /></Pressable>
    </View>
  );
}`,
		"src/Row.tsx": `import { Pressable, View } from 'react-native';
export function Row({ item }) {
  return (
    <View>
      <Pressable onPress={() => pick(item)}><Icon /></Pressable>
      <Badge item={item} />
    </View>
  );
}`,
		"src/Badge.tsx": `import { Pressable } from 'react-native';
export const Badge = ({ item }) => <Pressable onPress={() => star(item)}><Icon /></Pressable>;
export const Header = () => <Pressable onPress={back}><Icon /></Pressable>;`,
		"web/Grid.jsx": `export default function Grid({ tiles }) {
  return <ul>{tiles.map(t => <Tile key={t.id} tile={t} />)}</ul>;
}`,
		"web/Tile.jsx": `export default function Tile({ tile }) {
  return <li><button onClick={() => like(tile)}><svg /></button></li>;
}
export function Toolbar() {
  return <button onClick={share}><svg /></button>;
}`,
		"src/Old.tsx": `import { FlatList, Pressable } from 'react-native';
export class Old extends React.Component {
  renderRow = ({ item }) => <Pressable onPress={() => open(item)}><Icon /></Pressable>;
  renderFooter() {
    return <Pressable onPress={more}><Icon /></Pressable>;
  }
  render() {
    return <FlatList renderItem={this.renderRow} ListFooterComponent={this.renderFooter} />;
  }
}`,
		"web/TodoList.vue": `<template>
  <ul><todo-item v-for="t in todos" :key="t.id" :todo="t" /></ul>
</template>`,
		"web/TodoItem.vue": `<template>
  <li><button @click="done"><svg></svg></button></li>
</template>`,
	}
	dir := write(t, files)
	fixed, left := fixedAndLeft(fixDir(t, dir, false))
	expectFixed(t, fixed,
		`Badge.tsx testID="back-button"`,
		`List.tsx testID="refresh-button"`,
		`Old.tsx testID="more-button"`,
		`Tile.jsx data-testid="share-button"`,
	)
	expectLeftFor(t, left, "List.tsx", "renderRow", "renderItem")
	expectLeftFor(t, left, "Row.tsx", "Row", "List.tsx")
	expectLeftFor(t, left, "Badge.tsx", "Badge", "Row")
	expectLeftFor(t, left, "Tile.jsx", "Tile", "Grid.jsx")
	expectLeftFor(t, left, "Old.tsx", "renderRow", "renderItem")
	expectLeftFor(t, left, "TodoItem.vue", "TodoItem", "v-for", "TodoList.vue")
	if len(left) != 6 {
		t.Errorf("left %v, want only List.tsx, Row.tsx, Badge.tsx, Tile.jsx, Old.tsx and TodoItem.vue", left)
	}
}

// Issue #147: a composable that a lazy list in another file repeats (items { PostRow(it) })
// gets no fixed testTag; one no loop calls does.
func TestFixLeavesComposableRowsRenderedElsewhere(t *testing.T) {
	dir := write(t, map[string]string{
		"app/Main.kt": composeExposed,
		"app/Feed.kt": `import androidx.compose.foundation.lazy.LazyColumn
@Composable fun Feed(posts: List<Post>) {
    LazyColumn {
        items(posts) { post -> PostRow(post) }
    }
}`,
		"app/PostRow.kt": `import androidx.compose.material3.IconButton
@Composable fun PostRow(post: Post) {
    IconButton(onClick = { like(post) }) { Icon(Icons.Default.Favorite, null) }
}
@Composable fun TopBar(onClose: () -> Unit) {
    IconButton(onClick = onClose) { Icon(Icons.Default.Close, null) }
}`,
	})
	fixed, left := fixedAndLeft(fixDir(t, dir, false))
	expectFixed(t, fixed, `PostRow.kt modifier = Modifier.testTag("close-button")`)
	expectLeftFor(t, left, "PostRow.kt", "PostRow", "Feed.kt")
	if len(left) != 1 {
		t.Errorf("left %v, want only PostRow.kt", left)
	}
}

// Issue #147: a RecyclerView (or ListView) row layout is inflated once per item, so --fix
// gives its views no fixed android:id: the layouts an adapter inflates (by R.layout or by
// its view binding, in Kotlin or Java) and the ones a list previews with tools:listitem.
// A screen's own layout is still fixed.
func TestFixLeavesRecyclerViewRows(t *testing.T) {
	const ns = `xmlns:android="http://schemas.android.com/apk/res/android"`
	button := func(src string) string {
		return `<FrameLayout ` + ns + `>
    <ImageButton android:src="@drawable/` + src + `" />
</FrameLayout>
`
	}
	dir := write(t, map[string]string{
		"app/src/main/res/layout/activity_main.xml": `<FrameLayout ` + ns + `>
    <ImageButton android:onClick="openMenu" android:src="@drawable/ic_menu" />
</FrameLayout>
`,
		"app/src/main/res/layout/item_contact.xml": button("ic_call"),
		"app/src/main/res/layout/item_order.xml":   button("ic_cancel"),
		"app/src/main/res/layout/item_tag.xml":     button("ic_remove"),
		"app/src/main/res/layout/fragment_tags.xml": `<FrameLayout ` + ns + ` xmlns:tools="http://schemas.android.com/tools">
    <androidx.recyclerview.widget.RecyclerView tools:listitem="@layout/item_tag" />
</FrameLayout>
`,
		"app/src/main/java/com/x/ContactAdapter.kt": `package com.x
class ContactAdapter : RecyclerView.Adapter<VH>() {
    override fun onCreateViewHolder(parent: ViewGroup, viewType: Int) =
        VH(LayoutInflater.from(parent.context).inflate(R.layout.item_contact, parent, false))
}
class VH(v: View) : RecyclerView.ViewHolder(v)
class MainActivity : AppCompatActivity() {
    override fun onCreate(b: Bundle?) { super.onCreate(b); setContentView(R.layout.activity_main) }
}`,
		"app/src/main/java/com/x/OrderAdapter.java": `package com.x;
public class OrderAdapter extends ListAdapter<Order, OrderAdapter.Holder> {
    @Override public Holder onCreateViewHolder(ViewGroup parent, int type) {
        return new Holder(ItemOrderBinding.inflate(LayoutInflater.from(parent.getContext()), parent, false));
    }
}`,
	})
	fixed, left := fixedAndLeft(fixDir(t, dir, false))
	expectFixed(t, fixed, `activity_main.xml android:id="@+id/open_menu_button"`)
	expectLeftFor(t, left, "item_contact.xml", "ContactAdapter")
	expectLeftFor(t, left, "item_order.xml", "OrderAdapter")
	expectLeftFor(t, left, "item_tag.xml", "tools:listitem", "fragment_tags.xml")
	if len(left) != 3 {
		t.Errorf("left %v, want only the three rows", left)
	}
}

// Issue #147 review: only a class whose own supertype is an adapter or a view holder inflates
// rows: not an activity that implements a listener nested in an adapter
// (NoteAdapter.OnNoteListener), nor a fragment that takes an adapter in its constructor.
func TestFixRowLayoutsOnlyFromAdapters(t *testing.T) {
	const ns = `xmlns:android="http://schemas.android.com/apk/res/android"`
	button := func(click string) string {
		return `<FrameLayout ` + ns + `>
    <ImageButton android:onClick="` + click + `" android:src="@drawable/ic_x" />
</FrameLayout>
`
	}
	dir := write(t, map[string]string{
		"app/src/main/res/layout/activity_main.xml": button("openMenu"),
		"app/src/main/res/layout/fragment_home.xml": button("refresh"),
		"app/src/main/res/layout/item_note.xml":     button("deleteNote"),
		"app/src/main/java/com/x/MainActivity.java": `package com.x;
public class MainActivity extends AppCompatActivity implements NoteAdapter.OnNoteListener {
    @Override protected void onCreate(Bundle b) { super.onCreate(b); setContentView(R.layout.activity_main); }
}`,
		"app/src/main/java/com/x/HomeFragment.kt": `package com.x
class HomeFragment(private val adapter: PostAdapter) : Fragment(R.layout.fragment_home) {
    override fun onViewCreated(v: View, b: Bundle?) { FragmentHomeBinding.bind(v) }
}`,
		"app/src/main/java/com/x/NoteAdapter.java": `package com.x;
public class NoteAdapter extends RecyclerView.Adapter<NoteAdapter.Holder> implements Filterable {
    public interface OnNoteListener { void onNote(int i); }
    @Override public Holder onCreateViewHolder(ViewGroup p, int t) {
        return new Holder(LayoutInflater.from(p.getContext()).inflate(R.layout.item_note, p, false));
    }
}`,
	})
	fixed, left := fixedAndLeft(fixDir(t, dir, false))
	expectFixed(t, fixed,
		`activity_main.xml android:id="@+id/open_menu_button"`,
		`fragment_home.xml android:id="@+id/refresh_button"`,
	)
	expectLeftFor(t, left, "item_note.xml", "NoteAdapter")
	if len(left) != 1 {
		t.Errorf("left %v, want only item_note.xml", left)
	}
}

func TestRowClass(t *testing.T) {
	for header, want := range map[string]bool{
		` : RecyclerView.Adapter<VH>() {`:                                        true,
		`(v: View) : RecyclerView.ViewHolder(v)`:                                 true,
		` extends ListAdapter<Order, OrderAdapter.Holder> implements Filterable`: true,
		` extends Base implements Comparable<X>, PagingDataAdapter`:              true,
		` extends AppCompatActivity implements NoteAdapter.OnNoteListener`:       false,
		`(private val adapter: PostAdapter) : Fragment(R.layout.fragment_home)`:  false,
		`<A : BaseAdapter>(a: A) : Screen()`:                                     false,
		` : Fragment(), MyAdapter.Callback`:                                      false,
	} {
		if got := rowClass(header); got != want {
			t.Errorf("rowClass(%q) = %v, want %v", header, got, want)
		}
	}
}

// Issue #147 review: components that render each other (A renders B renders A) are read
// once each: the spread ends, and both are left when a loop repeats one of them.
func TestFixRowsCycle(t *testing.T) {
	dir := write(t, map[string]string{
		"src/List.tsx": `import { View } from 'react-native';
export const L = ({ items }) => <View>{items.map(i => <A key={i.id} />)}</View>;`,
		"src/A.tsx": `import { Pressable } from 'react-native';
export function A() { return <Pressable onPress={a}><Icon /><B /></Pressable>; }`,
		"src/B.tsx": `import { Pressable } from 'react-native';
export function B() { return <Pressable onPress={b}><Icon /><A /></Pressable>; }`,
	})
	fixed, left := fixedAndLeft(fixDir(t, dir, false))
	expectFixed(t, fixed)
	expectLeftFor(t, left, "A.tsx", "it is in A and the .map() in List.tsx:2 repeats A")
	expectLeftFor(t, left, "B.tsx", "it is in B, which A renders,")
}

// Issue #147 review: a render prop that is not a list's (a form field's render={…}) renders
// once: the component it names, and what that renders, keep their fixes. A namespaced
// component in a loop (<Menu.Item/>) is not every local Item. A JSX attribute on its own
// line (Header={…}) defines no Header.
func TestFixRowsOnlyFromLists(t *testing.T) {
	dir := write(t, map[string]string{
		"src/Form.tsx": `import { Pressable } from 'react-native';
export function Form() {
  const renderInput = ({ field }) => <Field field={field} />;
  return <Controller name="email" render={renderInput} />;
}
export function Field() { return <Pressable onPress={clear}><Icon /></Pressable>; }`,
		"src/Menu.tsx": `import { Pressable, View } from 'react-native';
export const M = ({ entries }) => <View>{entries.map(e => <Menu.Item key={e.id} />)}</View>;
function Item() { return <Pressable onPress={pick}><Icon /></Pressable>; }`,
		"src/Card.tsx": `import { Pressable, View } from 'react-native';
export const Card = ({ xs }) => (
  <View>
    {xs.map(x => <Header key={x} />)}
    <Panel
      Header={<Pressable onPress={save}><Icon /></Pressable>}
    />
  </View>
);`,
	})
	fixed, left := fixedAndLeft(fixDir(t, dir, false))
	expectFixed(t, fixed,
		`Card.tsx testID="save-button"`,
		`Form.tsx testID="clear-button"`,
		`Menu.tsx testID="pick-button"`,
	)
	if len(left) != 0 {
		t.Errorf("left %v, want nothing", left)
	}
}

// Issue #147 review: a line is found once per file, not by counting from its start for each
// offset (quadratic on a minified file's one long line).
func TestLinesAt(t *testing.T) {
	l := &lines{path: "src/a.js", src: "a\nbb\n\nc"}
	for off, want := range map[int]string{0: "a.js:1", 1: "a.js:1", 2: "a.js:2", 5: "a.js:3", 6: "a.js:4", 7: "a.js:4"} {
		if got := l.at(off); got != want {
			t.Errorf("at(%d) = %q, want %q", off, got, want)
		}
	}
}
