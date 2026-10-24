package geo

import (
	"math"
	"testing"
)

// 实测基准：地点记录（GCJ-02）与到访记录（WGS-84）的对照。
//
// 数值取自仓库内的脱敏样本 testdata/rond-export（合成地点名，坐标是真实
// 数据平移后的结果），语义与原始数据一致：同一地点在两张表里的坐标，
// 到访值 ≈ GCJ02ToWGS84(地点值)。
//
// 用 Go 的迭代逆变换实测，偏差在厘米级（0.004~0.09 m）——比坐标本身的
// 存储精度还小，说明换算实现与数据生成时的算法一致。
//
// 这些数值一旦被改动，说明坐标系实现被动过：测试会在这里失败。
var goldenPairs = []struct {
	name   string
	gcjLat float64
	gcjLon float64
	wgsLat float64
	wgsLon float64
}{
	{"场所-0943", 32.52126, 118.714897, 32.523343, 118.709664},
	{"场所-0813", 32.509339, 118.625336, 32.511572, 118.620275},
	{"场所-0611", 32.465148, 118.631096, 32.467384, 118.626032},
	{"场所-0357", 32.480484, 118.614199, 32.482731, 118.609149},
}

func TestGCJ02ToWGS84MatchesExportedVisits(t *testing.T) {
	for _, g := range goldenPairs {
		lat, lon := GCJ02ToWGS84(g.gcjLat, g.gcjLon)
		d := HaversineMeters(lat, lon, g.wgsLat, g.wgsLon)
		// 实测偏差在厘米级；留到 5 m 作为回归阈值。
		if d > 5 {
			t.Errorf("%s: GCJ02ToWGS84 结果与导出到访坐标相距 %.2f m，超过 5 m", g.name, d)
		}
	}
}

func TestRoundTripWithinCentimeter(t *testing.T) {
	for _, g := range goldenPairs {
		lat, lon := WGS84ToGCJ02(g.wgsLat, g.wgsLon)
		back, backLon := GCJ02ToWGS84(lat, lon)
		if d := HaversineMeters(back, backLon, g.wgsLat, g.wgsLon); d > 0.01 {
			t.Errorf("%s: 往返残差 %.4f m，超过 1 cm", g.name, d)
		}
	}
}

// TestOffsetMagnitude 守住「偏移量级」：在中国境内 GCJ-02 与 WGS-84 相差
// 数百米。若有人把变换写成了恒等函数，这个用例会立刻失败。
func TestOffsetMagnitude(t *testing.T) {
	for _, g := range goldenPairs {
		d := HaversineMeters(g.gcjLat, g.gcjLon, g.wgsLat, g.wgsLon)
		if d < 300 || d > 620 {
			t.Errorf("%s: 偏移量 %.0f m 落在 304–603 m 的实测区间之外", g.name, d)
		}
	}
}

func TestOutOfChinaIdentity(t *testing.T) {
	overseas := []struct {
		name     string
		lat, lon float64
	}{
		{"东京", 35.6812, 139.7671},
		{"新加坡", 1.3521, 103.8198},
		{"旧金山", 37.7749, -122.4194},
	}
	for _, o := range overseas {
		if !OutOfChina(o.lat, o.lon) {
			t.Fatalf("%s 应判定为境外", o.name)
		}
		lat, lon := GCJ02ToWGS84(o.lat, o.lon)
		if lat != o.lat || lon != o.lon {
			t.Errorf("%s: 境外坐标不应被变换，得到 (%v,%v)", o.name, lat, lon)
		}
	}
	// 混淆区内的点必须被判定为境内（用脱敏样本里的一个境内坐标）。
	if OutOfChina(32.52126, 118.714897) {
		t.Fatal("境内坐标应判定为境内")
	}
}

func TestHaversineSentinel(t *testing.T) {
	// (0,0) 是「无坐标」哨兵，不能因为算术上接近某个点而被判为很近。
	if d := HaversineMeters(0, 0, 31.3, 120.6); !math.IsInf(d, 1) {
		t.Fatalf("哨兵坐标应返回 +Inf，得到 %v", d)
	}
	// 1 度纬度约 111 km。
	if d := HaversineMeters(0, 0.0000001, 1, 0.0000001); d < 111000 || d > 111500 {
		t.Fatalf("1 度纬度应约 111 km，得到 %.0f m", d)
	}
}

func TestQuantize(t *testing.T) {
	cases := []struct {
		in     float64
		digits int
		want   float64
	}{
		{27.280000686645508, 4, 27.28}, // float32 噪声被抹平
		{0.64, 6, 0.64},                // 湿度不能被抹成 0
		{31.299758, 6, 31.299758},      // 坐标 6 位
		{120.585294123456, 6, 120.585294},
	}
	for _, c := range cases {
		if got := Quantize(c.in, c.digits); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Quantize(%v,%d) = %v, want %v", c.in, c.digits, got, c.want)
		}
	}
	// 语义见 IsFloat32Exact 的文档：精确等于某个 float32 即说明来自
	// float32 存储，带噪声；干净的 float64 值反而不等于任何 float32。
	if !IsFloat32Exact(27.280000686645508) {
		t.Error("27.280000686645508 应被识别为 float32 提升值（带噪声）")
	}
	if IsFloat32Exact(27.28) {
		t.Error("27.28 是干净的 float64，不应被识别为 float32 提升值")
	}
}
