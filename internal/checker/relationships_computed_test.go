package checker

import "testing"

func TestCheckComputedFieldsAndWithRelationships(t *testing.T) {
	valid := headerWithDB + `
model author {
  id uuid primary
  first_name string required
  last_name string required
  computed full_name string = first_name + " " + last_name
  computed display_name string = full_name + "!"
}
model post {
  id uuid primary
  author_id uuid ref(author)
  title string required
  computed title_length int = 1 + 1
}
GET /posts {
  |> posts = query post with(author)
  -> 200 { posts: posts }
}`
	expectNoErrors(t, check(t, valid))

	t.Run("unique target id", func(t *testing.T) {
		source := headerWithDB + `
model author { id uuid unique }
model post { id uuid primary author_id uuid ref(author) }
GET /posts { |> posts = query post with(author) -> 200 { posts: posts } }`
		expectNoErrors(t, check(t, source))
	})
}

func TestCheckRejectsInvalidComputedFieldsAndRelationships(t *testing.T) {
	tests := map[string]struct {
		body string
		want string
	}{
		"unknown relationship": {
			body: `model post { id uuid primary }
GET /posts { |> posts = query post with(author) -> 200 { posts: posts } }`,
			want: `no ref-backed relationship "author"`,
		},
		"dynamic relationship": {
			body: `model post { id uuid primary }
GET /posts { |> posts = query post with("author") -> 200 { posts: posts } }`,
			want: "relationships must be bare identifiers",
		},
		"with on fetch": {
			body: `model author { id uuid primary }
model post { id uuid primary author_id uuid ref(author) }
GET /posts { |> post = fetch post with(author) -> 200 { post: post } }`,
			want: "fetch does not support with",
		},
		"target without id": {
			body: `model author { slug string primary }
model post { id uuid primary author_id string ref(author) }
GET /posts { |> posts = query post with(author) -> 200 { posts: posts } }`,
			want: `targets model "author" without a persisted id field`,
		},
		"relationship type mismatch": {
			body: `model author { id uuid primary }
model post { id uuid primary author_id int ref(author) }
GET /posts { |> posts = query post with(author) -> 200 { posts: posts } }`,
			want: `relationship "author" has type int but author.id has type uuid`,
		},
		"relationship target id is not unique": {
			body: `model author { id uuid required }
model post { id uuid primary author_id uuid ref(author) }
GET /posts { |> posts = query post with(author) -> 200 { posts: posts } }`,
			want: `author.id, which is neither primary nor unique`,
		},
		"relationship field collision": {
			body: `model author { id uuid primary }
model post { id uuid primary author_id uuid ref(author) author json }
GET /posts { |> posts = query post with(author) -> 200 { posts: posts } }`,
			want: `relationship "author" collides with a field`,
		},
		"same target needs aliases": {
			body: `model user { id uuid primary }
model post { id uuid primary author_id uuid ref(user) editor_id uuid ref(user) }
GET /posts { |> posts = query post with(author, editor) -> 200 { posts: posts } }`,
			want: `both join model "user"`,
		},
		"self join needs alias": {
			body: `model category { id uuid primary parent_id uuid ref(category) }
GET /categories { |> rows = query category with(parent) -> 200 { rows: rows } }`,
			want: `self relationship "parent"`,
		},
		"forward computed reference": {
			body: `model person {
  id uuid primary
  computed label string = later
  computed later string = "later"
}`,
			want: `references unknown or later field "later"`,
		},
		"computed type mismatch": {
			body: `model person { id uuid primary computed label bool = 1 + 1 }`,
			want: "declares bool but its expression produces int",
		},
		"optional computed source": {
			body: `model person { id uuid primary nickname string optional computed label string = nickname + "!" }`,
			want: `cannot use nullable field "nickname"`,
		},
		"unconstrained computed source": {
			body: `model person { id uuid primary nickname string computed label string = nickname + "!" }`,
			want: `cannot use nullable field "nickname"`,
		},
		"default-only computed source": {
			body: `model person { id uuid primary nickname string default("anonymous") computed label string = nickname + "!" }`,
			want: `cannot use nullable field "nickname"`,
		},
		"unsupported computed source type": {
			body: `model person { id uuid primary computed label string = id }`,
			want: `cannot use field "id" of type uuid`,
		},
		"impure computed expression": {
			body: `model person { id uuid primary name string required computed label string = decorate(name) }`,
			want: "uses unsupported expression",
		},
		"duplicate model property": {
			body: `model person { id uuid primary name string computed name string = "x" }`,
			want: "duplicate field 'name'",
		},
		"computed assignment": {
			body: `model person { id uuid primary name string required computed label string = name + "!" }
GET /person {
  |> person = fetch person
  |> when true: person.label = "replacement"
  -> 200 { person: person }
}`,
			want: `cannot assign to computed field "label"`,
		},
		"paginate and first": {
			body: `model post { id uuid primary }
GET /posts { |> post = query post paginate(1, 10) first -> 200 { post: post } }`,
			want: "cannot combine paginate(...) and first",
		},
		"with and legacy block": {
			body: `model author { id uuid primary }
model post { id uuid primary author_id uuid ref(author) }
GET /posts { |> rows = query post { active: true } with(author) -> 200 { rows: rows } }`,
			want: "cannot be combined with legacy positional or block arguments",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			errors := check(t, headerWithDB+"\n"+test.body)
			expectErrorContaining(t, errors, test.want)
		})
	}
}

func TestCheckRejectsComputedFieldsInWriteBodies(t *testing.T) {
	model := `
model person {
  id uuid primary
  name string required
  computed label string = name + "!"
}
`
	tests := map[string]struct {
		body string
		want string
	}{
		"save": {
			body: `POST /people { |> person = save person { name: "Ada", label: "forged" } -> 201 { person: person } }`,
			want: `save cannot write computed field "label"`,
		},
		"seed": {
			body: `POST /seed { |> person = seed person { name: "Ada", label: "forged" } -> 201 { person: person } }`,
			want: `seed cannot write computed field "label"`,
		},
		"update model": {
			body: `PATCH /people { |> person = update person { label: "forged" } -> 200 { person: person } }`,
			want: `update cannot write computed field "label"`,
		},
		"update binding": {
			body: `PATCH /people/:id {
  <- id uuid required
  |> old = fetch person(id)
  |> updated = update old { label: "forged" }
  -> 200 { person: updated }
}`,
			want: `update cannot write computed field "label"`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			errors := check(t, headerWithDB+model+test.body)
			expectErrorContaining(t, errors, test.want)
		})
	}

	t.Run("unknown persisted key remains unchanged", func(t *testing.T) {
		errors := check(t, headerWithDB+model+`POST /people {
  |> person = save person { name: "Ada", future_field: "kept-compatible" }
  -> 201 { person: person }
}`)
		expectNoErrors(t, errors)
	})
}
