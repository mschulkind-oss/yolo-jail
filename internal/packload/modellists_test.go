package packload

// modellists_test.go pins how a launch composes the `models` kind into the providers table
// (docs/design/model-lists-and-pickers.md §7.2, OQ-BR12): through ComposeProviders, the one
// composition every notch and `yolo check` run, so a test here fails when the call site that
// applies the kind is deleted, not only when the helper is.

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// gatewayPack ships a provider with an unordered two-model list and a `default` alias, the
// shape packs/zai ships: no `order` facts anywhere.
func gatewayPack(t *testing.T) *Pack {
	return &Pack{Name: "gateway", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"gw",
	   "endpoints":{"openai":{"base_url":"https://gw.example/v1","wire_api":"openai-chat-completions"}},
	   "models":{"default":"beta-2","alpha-1":"alpha-1","beta-2":"beta-2"},
	   "model_options":{"beta-2":{"name":"Beta 2"}}}]}`)}
}

func modelsPack(t *testing.T, name, contributes string) *Pack {
	return &Pack{Name: name, Decl: declFrom(t, `{"contributes":[`+contributes+`]}`)}
}

// composedList reads a provider's list back in the consumers' order (codexModelList's rule:
// the `order` fact, declared before undeclared, then id), one id per row.
func composedList(t *testing.T, table *jsonx.OrderedMap, provider string) []string {
	t.Helper()
	entry := providerEntry(table, provider)
	if entry == nil {
		t.Fatalf("the composed table has no %s: %s", provider, dump(t, table))
	}
	models := subOrderedOrNil(entry, "models")
	opts := subOrderedOrNil(entry, "model_options")
	type row struct {
		id      string
		order   string
		ordered bool
	}
	seen := map[string]*row{}
	var rows []*row
	if models == nil {
		return nil
	}
	aliases := append([]string(nil), models.Keys()...)
	for pass := 0; pass < 2; pass++ {
		for _, a := range aliases {
			id, _ := stringAt(models, a)
			if (pass == 0) != (id == a) {
				continue
			}
			r := seen[id]
			if r == nil {
				r = &row{id: id}
				seen[id] = r
				rows = append(rows, r)
			}
			if !r.ordered {
				if o, ok := stringAt(subOrderedOrNil(opts, a), "order"); ok {
					r.order, r.ordered = o, true
				}
			}
		}
	}
	for i := 1; i < len(rows); i++ {
		for j := i; j > 0; j-- {
			a, b := rows[j-1], rows[j]
			less := func() bool {
				if a.ordered != b.ordered {
					return a.ordered
				}
				if a.ordered && a.order != b.order {
					return len(a.order) < len(b.order) || (len(a.order) == len(b.order) && a.order < b.order)
				}
				return a.id < b.id
			}
			if !less() {
				rows[j-1], rows[j] = b, a
			}
		}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.id)
	}
	return out
}

func composeWithNotes(t *testing.T, user *jsonx.OrderedMap, packs []*Pack) (*jsonx.OrderedMap, []string) {
	t.Helper()
	var notes []string
	got, err := ComposeProviders(user, packs, WithModelNotes(func(n string) { notes = append(notes, n) }))
	if err != nil {
		t.Fatalf("composing: %v", err)
	}
	return got, notes
}

// AN `add` APPENDS, after the provider's own list in the order the consumers show it, and its
// entry lands in the shape they already read: the id under itself, its alias beside it, its
// facts in model_options in the flat vocabulary, vendor and description included.
func TestAModelsAddAppendsToAnotherPacksList(t *testing.T) {
	company := modelsPack(t, "company", `{"kind":"models","provider":"gw","add":[
	  {"id":"kimi-k3","vendor":"moonshot","alias":"kimi","name":"Kimi K3","description":"Long context",
	   "context_window":256000,"reasoning":true,"input":["text","image"]}]}`)
	table, notes := composeWithNotes(t, nil, []*Pack{gatewayPack(t), company})
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none", notes)
	}
	if got, want := composedList(t, table, "gw"), []string{"alpha-1", "beta-2", "kimi-k3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("gw list = %v, want the provider's own two, then the added one", got)
	}
	entry := providerEntry(table, "gw")
	models := subOrderedOrNil(entry, "models")
	if id, _ := stringAt(models, "kimi"); id != "kimi-k3" {
		t.Errorf("models.kimi = %q, want the alias to name the added id", id)
	}
	if id, _ := stringAt(models, "default"); id != "beta-2" {
		t.Errorf("models.default = %q, want the provider's own default untouched", id)
	}
	facts := subOrderedOrNil(subOrderedOrNil(entry, "model_options"), "kimi-k3")
	want := map[string]string{"vendor": "moonshot", "name": "Kimi K3", "description": "Long context",
		"context_window": "256000", "reasoning": "true", "input": "text,image"}
	for k, v := range want {
		if got, _ := stringAt(facts, k); got != v {
			t.Errorf("model_options.kimi-k3.%s = %q, want %q", k, got, v)
		}
	}
	if _, marked := entry.Get(ModelsOnlyKey); marked {
		t.Errorf("an add marked the list %s; only an `only` narrows it", ModelsOnlyKey)
	}
}

// EVERY `add` APPLIES BEFORE EVERY `only`, whatever the pack order (MM-D11): a company's
// narrowing listed first in `packs` cannot be re-opened by a pack listed after it. Two onlys
// intersect, and a narrowed list carries the mark the derives render an exact menu from.
func TestModelsOnlyNarrowsAfterEveryAdd(t *testing.T) {
	narrow := modelsPack(t, "policy", `{"kind":"models","provider":"gw","only":["beta-2","kimi-k3","grok-5"]},
	  {"kind":"models","provider":"gw","only":["beta-2","kimi-k3"]}`)
	adder := modelsPack(t, "extras", `{"kind":"models","provider":"gw","add":[
	  {"id":"kimi-k3","vendor":"moonshot"},{"id":"glm-6","vendor":"zai"}]}`)
	table, notes := composeWithNotes(t, nil, []*Pack{gatewayPack(t), narrow, adder})
	if got, want := composedList(t, table, "gw"), []string{"beta-2", "kimi-k3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("gw list = %v, want %v: the intersection of both onlys over every add", got, want)
	}
	entry := providerEntry(table, "gw")
	if v, _ := entry.Get(ModelsOnlyKey); v != true {
		t.Errorf("%s = %v, want true on a list an only narrowed", ModelsOnlyKey, v)
	}
	if id, _ := stringAt(subOrderedOrNil(entry, "models"), "default"); id != "beta-2" {
		t.Errorf("models.default = %q, want the kept default's alias kept", id)
	}
	if _, kept := subOrderedOrNil(entry, "models").Get("alpha-1"); kept {
		t.Error("alpha-1 survived an only that does not name it")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], `"grok-5"`) {
		t.Errorf("notes = %v, want one naming grok-5, which the only names and nothing added", notes)
	}
}

// THE USER WRITES LAST (OQ-BR12's ruling): the user's own `providers.<name>.models` composes
// over the shaped list per alias, as it composes over a provider's own list — adding a model
// after an only, and deleting one with a null.
func TestTheUsersModelsWriteAfterEveryContribution(t *testing.T) {
	narrow := modelsPack(t, "policy", `{"kind":"models","provider":"gw","only":["beta-2","kimi-k3"]}`)
	adder := modelsPack(t, "extras", `{"kind":"models","provider":"gw","add":[{"id":"kimi-k3","vendor":"moonshot"}]}`)
	user := userProviders(t, `{"gw":{"models":{"mine-1":"mine-1","kimi-k3":null}}}`)
	table, notes := composeWithNotes(t, user, []*Pack{gatewayPack(t), narrow, adder})
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none", notes)
	}
	if got, want := composedList(t, table, "gw"), []string{"beta-2", "mine-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("gw list = %v, want the narrowed list, less the id the user deleted, plus the user's own", got)
	}
}

// A DUPLICATE KEEPS THE FIRST WRITER'S ENTRY WHOLE, position and facts, and check names it.
func TestADuplicateAddKeepsTheFirstWriter(t *testing.T) {
	first := modelsPack(t, "first", `{"kind":"models","provider":"gw","add":[{"id":"kimi-k3","vendor":"moonshot","name":"First"}]}`)
	second := modelsPack(t, "second", `{"kind":"models","provider":"gw","add":[
	  {"id":"beta-2","vendor":"gw","name":"Second's Beta"},{"id":"kimi-k3","vendor":"moonshot","name":"Second"}]}`)
	table, notes := composeWithNotes(t, nil, []*Pack{gatewayPack(t), first, second})
	opts := subOrderedOrNil(providerEntry(table, "gw"), "model_options")
	if name, _ := stringAt(subOrderedOrNil(opts, "kimi-k3"), "name"); name != "First" {
		t.Errorf("kimi-k3's name = %q, want the first writer's", name)
	}
	if name, _ := stringAt(subOrderedOrNil(opts, "beta-2"), "name"); name != "Beta 2" {
		t.Errorf("beta-2's name = %q, want the provider's own", name)
	}
	if len(notes) != 2 {
		t.Errorf("notes = %v, want one per duplicate", notes)
	}
}

// A PROVIDER NOBODY DECLARES IS SHAPED BY NOTHING, and says so; one only the user declares is
// shaped, and the user's entry composes over what the packs added.
func TestModelsNameAProviderByItsComposedEntry(t *testing.T) {
	company := modelsPack(t, "company", `{"kind":"models","provider":"mine","add":[{"id":"m-1","vendor":"me"}]}`)
	table, notes := composeWithNotes(t, nil, []*Pack{company})
	if table != nil && providerEntry(table, "mine") != nil {
		t.Errorf("a contribution for an undeclared provider created one: %s", dump(t, table))
	}
	if len(notes) != 1 || !strings.Contains(notes[0], `"mine"`) {
		t.Errorf("notes = %v, want one naming the undeclared provider", notes)
	}

	user := userProviders(t, `{"mine":{"endpoints":{"openai":{"base_url":"http://127.0.0.1:9/v1"}},"models":{"default":"m-0"}}}`)
	table, notes = composeWithNotes(t, user, []*Pack{company})
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none", notes)
	}
	if got, want := composedList(t, table, "mine"), []string{"m-1", "m-0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("mine list = %v, want %v: the pack's add, then the user's own", got, want)
	}
	if _, has := subOrderedOrNil(providerEntry(table, "mine"), "endpoints").Get("openai"); !has {
		t.Error("the user's own endpoint was lost")
	}
}

// NOTHING CHANGES FOR A PROVIDER NO CONTRIBUTION NAMES: its entry composes byte-identically,
// with no `order` fact written, so every launch without the kind renders what it rendered.
func TestAnUnshapedProviderComposesAsBefore(t *testing.T) {
	company := modelsPack(t, "company", `{"kind":"models","provider":"other","add":[{"id":"x","vendor":"v"}]}`)
	without := compose(t, nil, []*Pack{gatewayPack(t)})
	with, _ := composeWithNotes(t, nil, []*Pack{gatewayPack(t), company})
	if a, b := dump(t, providerEntry(without, "gw")), dump(t, providerEntry(with, "gw")); a != b {
		t.Errorf("an unshaped provider changed:\n  without the kind: %s\n  with it:          %s", a, b)
	}
}

// A USER'S OBJECT-FORM ENTRY CARRIES A DESCRIPTION the way a pack's `models` entry does
// (§7.3): liftModelFacts lowers it into model_options beside the name, where claude's picker
// reads its second line.
func TestAUsersModelDescriptionIsLifted(t *testing.T) {
	user := userProviders(t, `{"gw":{"models":{"beta-2":{"id":"beta-2","description":"Balanced"}}}}`)
	table := compose(t, user, []*Pack{gatewayPack(t)})
	facts := subOrderedOrNil(subOrderedOrNil(providerEntry(table, "gw"), "model_options"), "beta-2")
	if d, _ := stringAt(facts, "description"); d != "Balanced" {
		t.Errorf("model_options.beta-2.description = %q, want Balanced", d)
	}
	if n, _ := stringAt(facts, "name"); n != "Beta 2" {
		t.Errorf("model_options.beta-2.name = %q, want the pack's name kept under the user's facts", n)
	}
}

// THE FOOTPRINT NAMES EACH CONTRIBUTION, so `yolo pack footprint` and `pack lint` say which
// provider's list a pack shapes and how, before any launch; never review-worthy.
func TestAModelsContributionIsAFootprintClaim(t *testing.T) {
	p := modelsPack(t, "company", `{"kind":"models","provider":"gw","add":[{"id":"kimi-k3","vendor":"moonshot"}]},
	  {"kind":"models","provider":"gw","only":["kimi-k3"]}`)
	var details []string
	for _, c := range FootprintOf(p).Claims {
		if c.Kind != "models" {
			continue
		}
		if c.Target != "gw" || c.ReviewWorthy {
			t.Errorf("claim = %+v, want target gw, not review-worthy", c)
		}
		details = append(details, c.Detail)
	}
	joined := strings.Join(details, " | ")
	if len(details) != 2 || !strings.Contains(joined, "adds kimi-k3") || !strings.Contains(joined, "keeps only kimi-k3") {
		t.Errorf("models claims = %q, want one per contribution naming what it does", details)
	}
}

// AN `only` DROPS EVERY ENTRY IT DOES NOT NAME, ADJACENT ONES INCLUDED. The drop loop once
// ranged over models.Keys(), the map's own slice, while Delete shifted it in place, so the entry
// after each dropped one slid into the dropped slot unexamined and survived the narrowing, and
// with it a model the company removed reached every menu and claude's allowlist. Two cases: the
// shipped openai-codex list narrowed to one entry, where the two it drops sit next to each
// other, and a synthetic list of three where only the last is kept.
func TestAModelsOnlyDropsAdjacentEntries(t *testing.T) {
	shipped := modelsPack(t, "company", `{"kind":"models","provider":"openai-codex","only":["gpt-6.1-sol"]}`)
	packs := append(embeddedNamed(t, "openai-auth"), shipped)
	table, notes := composeWithNotes(t, nil, packs)
	if len(notes) != 0 {
		t.Errorf("notes = %v, want none", notes)
	}
	if got, want := composedList(t, table, "openai-codex"), []string{"gpt-6.1-sol"}; !reflect.DeepEqual(got, want) {
		t.Errorf("openai-codex list = %v, want %v: every entry the only does not name is dropped", got, want)
	}

	three := &Pack{Name: "three", Decl: declFrom(t, `{"contributes":[
	  {"kind":"provider","name":"gw3",
	   "endpoints":{"openai":{"base_url":"https://gw3.example/v1","wire_api":"openai-chat-completions"}},
	   "models":{"a-1":"a-1","b-1":"b-1","c-1":"c-1"}}]}`)}
	narrow := modelsPack(t, "policy", `{"kind":"models","provider":"gw3","only":["c-1"]}`)
	table, _ = composeWithNotes(t, nil, []*Pack{three, narrow})
	if got, want := composedList(t, table, "gw3"), []string{"c-1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("gw3 list = %v, want %v", got, want)
	}
	opts := subOrderedOrNil(providerEntry(table, "gw3"), "model_options")
	for _, dropped := range []string{"a-1", "b-1"} {
		if _, kept := opts.Get(dropped); kept {
			t.Errorf("model_options.%s survived the only that dropped its model", dropped)
		}
	}
}
