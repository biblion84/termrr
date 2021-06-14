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
	revenue       float64
	subscriptions int
	key           string
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

	var mrr float64

	p := &stripe.CustomerListParams{}
	p.Filters.AddFilter("limit", "", "100")
	p.AddExpand("data.subscriptions")
	p.AddExpand("data.discount")
	months := make(map[string]monthstats) // [YEAR-MM]

	customers := customer.List(p)
	for customers.Next() {
		var customerRevenue float64
		c := customers.Customer()

		if len(c.Subscriptions.Data) > 0 {
			subs := c.Subscriptions.Data

			for _, s := range subs {
				var subscriptionRevenue float64

				now := time.Now()
				nextYear := time.Date(now.Year()+1, now.Month(), 0, 0, 0, 0, 0, now.Location())

				if s.CancelAtPeriodEnd && time.Unix(s.CanceledAt, 0).Before(nextYear) {
					continue
				}
				if s.Status != stripe.SubscriptionStatusActive {
					continue
				}

				switch s.Plan.BillingScheme {
				case stripe.PlanBillingSchemeTiered:
					{
						priceParam := &stripe.PriceParams{}
						priceParam.AddExpand("tiers")
						plan, err := price.Get(s.Plan.ID, priceParam)
						if err != nil {
							log.Fatal(err)
						}
						if plan.TiersMode == stripe.PriceTiersModeGraduated {
							for i := int64(1); i <= s.Quantity; i++ { // Hopefully the array is sorted desc from upTo
								for _, tier := range plan.Tiers {
									if i <= tier.UpTo || tier.UpTo == 0 {
										subscriptionRevenue += tier.UnitAmountDecimal
										break
									}
								}
							}
						}
						if s.Plan.Interval == stripe.PlanIntervalYear {
							subscriptionRevenue = float64(subscriptionRevenue) / 12.0
						}
						subscriptionRevenue = applyDiscount(subscriptionRevenue, s.Discount)

						customerRevenue += subscriptionRevenue
					}
				default:
					log.Println("UNSUPPORTED BILLING SCHEME")
				}
				// logging monthly stats
				d := time.Unix(s.StartDate, 0)
				monthKey := d.Format("2006-01")

				if stats, ok := months[monthKey]; ok {
					stats.revenue += subscriptionRevenue / 100.0
					stats.subscriptions++
					months[monthKey] = stats
				} else {
					stats = monthstats{subscriptions: 1, revenue: subscriptionRevenue / 100.0, key: monthKey}
					months[monthKey] = stats
				}

			}
			customerRevenue = applyDiscount(customerRevenue, c.Discount)

			mrr += customerRevenue / 100.0

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
		fmt.Println(k, fmt.Sprintf("New subscriptions: %d", months[k].subscriptions), fmt.Sprintf("MRR: %.2f", months[k].revenue))
	}
}

func applyDiscount(v float64, discount *stripe.Discount) float64 {
	if discount == nil || discount.Coupon == nil {
		return v
	}

	// If the discount end in less than a year, we don't take it into account for the mrr
	now := time.Now()
	nextYear := time.Date(now.Year()+1, now.Month(), 0, 0, 0, 0, 0, now.Location())
	discountEnd := time.Unix(discount.End, 0)
	if discountEnd.Before(nextYear) {
		return v
	}

	coupon := discount.Coupon
	if coupon.AmountOff > 0 {
		return v - float64(coupon.AmountOff)
	} else if coupon.PercentOff > 0 {
		return v - ((float64(coupon.PercentOff) / 100.0) * v)
	}
	return v
}
