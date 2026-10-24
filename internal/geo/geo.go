// Package geo 提供中国区坐标偏移修正与球面距离计算。
//
// 游履的两份源数据使用不同坐标系，这是本包存在的全部理由：
//   - Rond 导出的「地点记录」是 GCJ-02（火星坐标）
//   - Rond 导出的「到访记录」是 WGS-84（真实坐标）
//
// 实测 4280 条到访记录中 4250 条（99.3%）的坐标，恰好等于地点记录中同名
// 点位经 GCJ02ToWGS84 变换后的结果（中位偏差 0.64 m、最大 3.12 m）；两者
// 在中国境内相差 304–603 m。不归一化会在地图上把同一个点分裂成相距约
// 470 米的两处。
package geo

import "math"

const (
	// geoA：Krasovsky 1940 长半轴；geoEE：偏心率平方。
	geoA  = 6378245.0
	geoEE = 0.00669342162296594323

	earthRadiusM = 6371000.0
)

// OutOfChina 报告坐标是否在 GCJ-02 混淆区（中国大陆）之外。
// 区域外（如海外，Apple Maps 原生 WGS-84）不做任何变换。
func OutOfChina(lat, lng float64) bool {
	return !(lng > 73.66 && lng < 135.05 && lat > 3.86 && lat < 53.55)
}

func transformLat(x, y float64) float64 {
	ret := -100.0 + 2.0*x + 3.0*y + 0.2*y*y + 0.1*x*y + 0.2*math.Sqrt(math.Abs(x))
	ret += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	ret += (20.0*math.Sin(y*math.Pi) + 40.0*math.Sin(y/3.0*math.Pi)) * 2.0 / 3.0
	ret += (160.0*math.Sin(y/12.0*math.Pi) + 320*math.Sin(y*math.Pi/30.0)) * 2.0 / 3.0
	return ret
}

func transformLng(x, y float64) float64 {
	ret := 300.0 + x + 2.0*y + 0.1*x*x + 0.1*x*y + 0.1*math.Sqrt(math.Abs(x))
	ret += (20.0*math.Sin(6.0*x*math.Pi) + 20.0*math.Sin(2.0*x*math.Pi)) * 2.0 / 3.0
	ret += (20.0*math.Sin(x*math.Pi) + 40.0*math.Sin(x/3.0*math.Pi)) * 2.0 / 3.0
	ret += (150.0*math.Sin(x/12.0*math.Pi) + 300.0*math.Sin(x/30.0*math.Pi)) * 2.0 / 3.0
	return ret
}

// WGS84ToGCJ02 把 WGS-84 坐标转为 GCJ-02。
func WGS84ToGCJ02(lat, lng float64) (float64, float64) {
	if OutOfChina(lat, lng) {
		return lat, lng
	}
	dLat := transformLat(lng-105.0, lat-35.0)
	dLng := transformLng(lng-105.0, lat-35.0)
	radLat := lat / 180.0 * math.Pi
	magic := math.Sin(radLat)
	magic = 1 - geoEE*magic*magic
	sqrtMagic := math.Sqrt(magic)
	dLat = (dLat * 180.0) / ((geoA * (1 - geoEE)) / (magic * sqrtMagic) * math.Pi)
	dLng = (dLng * 180.0) / (geoA / sqrtMagic * math.Cos(radLat) * math.Pi)
	return lat + dLat, lng + dLng
}

// GCJ02ToWGS84 把 GCJ-02（火星坐标）转为 WGS-84。逆解用两轮定点迭代，
// 残差远小于厘米级。
//
// 实现与 guanfu/internal/model/geo.go 逐行等价（该实现由 mujian 的差分
// 测试验证过），保证跨仓库结果一致。
func GCJ02ToWGS84(lat, lng float64) (float64, float64) {
	if OutOfChina(lat, lng) {
		return lat, lng
	}
	wLat, wLng := lat, lng
	for i := 0; i < 2; i++ {
		gLat, gLng := WGS84ToGCJ02(wLat, wLng)
		wLat -= gLat - lat
		wLng -= gLng - lng
	}
	return wLat, wLng
}

// HaversineMeters 返回两个经纬度之间的球面距离（米）。
// 两点均按 WGS-84 解释；任一坐标为 0 值哨兵时返回 +Inf，避免把「未知」
// 误判成「很近」。
func HaversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	if lat1 == 0 && lon1 == 0 || lat2 == 0 && lon2 == 0 {
		return math.Inf(1)
	}
	dLat := (lat2 - lat1) * math.Pi / 180.0
	dLon := (lon2 - lon1) * math.Pi / 180.0
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*math.Pi/180.0)*math.Cos(lat2*math.Pi/180.0)*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(a))
}

// Quantize 按小数位数四舍五入，消除 float32 导出噪声。
//
// Rond 导出的温度是 float32 精度（如 27.280000686645508），直接存库会让
// 冲突比对出现「22.34 vs 22.34000015258789」这种无意义差异。
func Quantize(v float64, digits int) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return v
	}
	p := math.Pow(10, float64(digits))
	return math.Round(v*p) / p
}

// IsFloat32Exact 判断 v 是否精确等于某个 float32 值。
//
// 语义与直觉相反，这里特意说明：Rond 以 float32 存储温度等数值，导出时
// 提升为 float64，于是 CSV 里出现 27.280000686645508 这样的串——它精确
// 等于 float32(27.28) 的提升值，因此本函数返回 true，表示「该值带有
// float32 精度噪声，应被 Quantize 抹平」。而干净的 27.28（float64 直写）
// 反而不等于任何 float32，返回 false。
//
// 实测：CSV 的温度、体感温度、露点、风速等列全部命中 true。
func IsFloat32Exact(v float64) bool { return float64(float32(v)) == v }
