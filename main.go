package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"sort"

	"github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/customer"
	"github.com/stripe/stripe-go/v72/price"
)

type monthstats struct {
	revenue   float64
	customers int
	key       string
}

func main() {
	key := flag.String("key", "", "this is your stripe key, you may use an environment variable STRIPE_KEY.")
	flag.Parse()

	apiKey := os.Getenv("STRIPE_KEY")

	if *key != "" {
		apiKey = *key
	}

	if apiKey == "" {
		flag.Usage()
		return
	}

	stripe.Key = apiKey
	//stripe.DefaultLeveledLogger = &stripe.LeveledLogger{
	//	Level: stripe.LevelDebug,
	//}

	var mrr float64

	p := &stripe.CustomerListParams{}
	p.Filters.AddFilter("limit", "", "100")
	p.AddExpand("data.subscriptions")
	p.AddExpand("data.discount")
	months := make(map[string]monthstats)

	customers := customer.List(p)
	for customers.Next() {
		c := customers.Customer()

		if len(c.Subscriptions.Data) > 0 {
			subs := c.Subscriptions.Data

			var revenue float64
			for _, s := range subs {
				now := time.Now()
				nextYear := time.Date(now.Year()+1, now.Month(), 0, 0, 0, 0, 0, now.Location())
				if s.CancelAtPeriodEnd && time.Unix(s.CanceledAt, 0).Before(nextYear) {
					continue
				}
				if s.Plan.BillingScheme == stripe.PlanBillingSchemeTiered && s.Status == stripe.SubscriptionStatusActive {
					priceParam := &stripe.PriceParams{}
					priceParam.AddExpand("tiers")
					plan, err := price.Get(s.Plan.ID, priceParam)
					if err != nil {
						log.Fatal(err)
					}
					var tempRevenue float64
					if plan.TiersMode == stripe.PriceTiersModeGraduated {
						for i := int64(1); i <= s.Quantity; i++ { // Hopefully the array is sorted desc from upTo
							for _, tier := range plan.Tiers {
								if i <= tier.UpTo || tier.UpTo == 0 {
									tempRevenue += tier.UnitAmountDecimal
									break
								}
							}
						}

					}

					if s.Plan.Interval == stripe.PlanIntervalYear {
						revenue += float64(tempRevenue) / 12.0
					} else {
						revenue += tempRevenue
					}
				}

				if c.Discount != nil && c.Discount.Coupon != nil {
					couponEnd := time.Unix(c.Discount.End, 0)
					if couponEnd.After(nextYear) {
						revenue = applyCoupon(revenue, c.Discount.Coupon)
					}
				}

				if s.Discount != nil && s.Discount.Coupon != nil {
					couponEnd := time.Unix(s.Discount.End, 0)
					if couponEnd.After(nextYear) {
						revenue = applyCoupon(revenue, s.Discount.Coupon)
					}
				}
			}

			mrr += revenue / 100.0

			// logging monthly stats
			d := time.Unix(c.Created, 0)
			monthKey := d.Format("2006-01")

			if stats, ok := months[monthKey]; ok {
				stats.revenue += revenue / 100.0
				stats.customers++
				months[monthKey] = stats
			} else {
				stats = monthstats{customers: 1, revenue: revenue / 100.0, key: monthKey}
				months[monthKey] = stats
			}
		} else {
			log.Println("this customer has no subs")
		}
	}

	var keys []string
	for _, v := range months {
		keys = append(keys, v.key)
	}

	sort.Strings(keys)

	fmt.Printf("MRR is\t%.2f\n", mrr)
	fmt.Println("Month over month stats\n=====================================")
	for i := len(keys) - 1; i >= 0; i-- {
		k := keys[i]
		fmt.Println(k, fmt.Sprintf("New customers: %d", months[k].customers), fmt.Sprintf("MRR: %.2f", months[k].revenue))
	}
}

func applyCoupon(v float64, coupon *stripe.Coupon) float64 {
	if coupon.AmountOff > 0 {
		return v - float64(coupon.AmountOff)
	} else if coupon.PercentOff > 0 {
		return v - ((float64(coupon.PercentOff) / 100.0) * v)
	}
	return v
}
