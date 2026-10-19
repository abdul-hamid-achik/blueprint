package python_test

import (
	"strings"
	"testing"
)

const modelBoundsBlueprint = `blueprint "bounds" { version "1.0" port 3000 runtime node database postgres }
model product {
  id    uuid   primary
  name  string required min(3) max(50)
  code  string optional max(8)
  qty   int    default(0) min(0) max(1000)
  price float  min(0.5) max(9.5)
  tags  list(string) min(1) max(5)
  flag  bool   min(1)
}
`

func TestPython_ModelFieldMinMaxEmitsPydanticBounds(t *testing.T) {
	outDir := buildPython(t, modelBoundsBlueprint)
	pyd := readPy(t, outDir, "src/models/pydantic.py")
	for _, want := range []string{
		"from pydantic import BaseModel, ConfigDict, Field\n",
		"from typing import Annotated, Optional\n",
		"    name: Annotated[str, Field(min_length=3, max_length=50)]\n",
		"    code: Optional[Annotated[str, Field(max_length=8)]]\n",
		"    qty: Annotated[int, Field(ge=0, le=1000)]\n",
		"    price: Annotated[float, Field(ge=0.5, le=9.5)]\n",
		"    tags: Annotated[list[str], Field(min_length=1, max_length=5)]\n",
	} {
		if !strings.Contains(pyd, want) {
			t.Errorf("src/models/pydantic.py missing %q:\n%s", want, pyd)
		}
	}
	// bool carries no bounds on the Python target, so it stays a plain annotation.
	if !strings.Contains(pyd, "    flag: bool\n") {
		t.Errorf("bool field must not get Field bounds:\n%s", pyd)
	}
}

func TestPython_ModelWithoutBoundsKeepsPlainPydanticImports(t *testing.T) {
	outDir := buildPython(t, `blueprint "plain" { version "1.0" port 3000 runtime node database postgres }
model note {
  id   uuid   primary
  body string required
}
`)
	pyd := readPy(t, outDir, "src/models/pydantic.py")
	if !strings.Contains(pyd, "from pydantic import BaseModel, ConfigDict\n") {
		t.Errorf("model without bounds must not import Field:\n%s", pyd)
	}
	if strings.Contains(pyd, "Annotated") {
		t.Errorf("model without bounds must not import Annotated:\n%s", pyd)
	}
}
