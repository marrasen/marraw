# toy.onnx

Tiny 2-op model (`y = relu(x + 1)` on a 1×3×2×2 float input, 127 bytes) so CI
exercises real ONNX Runtime kernels on every platform without downloading
production weights. Regenerate with `pip install onnx` and:

```python
import onnx
from onnx import helper, TensorProto

shape = [1, 3, 2, 2]
one = helper.make_tensor("one", TensorProto.FLOAT, [], [1.0])
add = helper.make_node("Add", ["x", "one"], ["t"])
relu = helper.make_node("Relu", ["t"], ["y"])
graph = helper.make_graph(
    [add, relu], "marraw_toy",
    [helper.make_tensor_value_info("x", TensorProto.FLOAT, shape)],
    [helper.make_tensor_value_info("y", TensorProto.FLOAT, shape)],
    initializer=[one],
)
model = helper.make_model(graph, opset_imports=[helper.make_opsetid("", 13)])
model.ir_version = 8
onnx.checker.check_model(model)
onnx.save(model, "toy.onnx")
```
